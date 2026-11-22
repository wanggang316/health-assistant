package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/wanggang316/health-assistant/server/internal/auth"
	"github.com/wanggang316/health-assistant/server/internal/quota"
	"github.com/wanggang316/health-assistant/server/internal/store"
	"github.com/wanggang316/health-assistant/server/internal/target"
)

type macroSplitJSON struct {
	ProteinPct int `json:"protein_pct"`
	FatPct     int `json:"fat_pct"`
	CarbPct    int `json:"carb_pct"`
}

type profileJSON struct {
	Sex           string          `json:"sex"`
	BirthYear     int             `json:"birth_year"`
	HeightCm      float64         `json:"height_cm"`
	WeightKg      float64         `json:"weight_kg"`
	ActivityLevel string          `json:"activity_level"`
	Goal          string          `json:"goal"`
	MacroSplit    *macroSplitJSON `json:"macro_split"`
}

type quotaJSON struct {
	Limit    int       `json:"limit"`
	Used     int       `json:"used"`
	ResetsAt time.Time `json:"resets_at"`
}

type meResponse struct {
	User             userJSON      `json:"user"`
	Profile          *profileJSON  `json:"profile"`
	DailyTarget      *target.Daily `json:"daily_target"`
	RecognitionQuota quotaJSON     `json:"recognition_quota"`
}

// Default macro split (ADR 0010).
var defaultSplit = macroSplitJSON{ProteinPct: 25, FatPct: 30, CarbPct: 45}

func (p profileJSON) validate(now time.Time) error {
	switch {
	case p.Sex != "male" && p.Sex != "female":
		return errInvalid("sex", "sex must be male or female.")
	case p.BirthYear < 1900 || p.BirthYear > now.Year():
		return errInvalid("birth_year", "birth_year is out of range.")
	case p.HeightCm < 100 || p.HeightCm > 250:
		return errInvalid("height_cm", "height_cm must be from 100 to 250.")
	case p.WeightKg < 30 || p.WeightKg > 300:
		return errInvalid("weight_kg", "weight_kg must be from 30 to 300.")
	}
	if _, ok := target.ActivityFactors[p.ActivityLevel]; !ok {
		return errInvalid("activity_level", "Unknown activity_level.")
	}
	if _, ok := target.GoalFactors[p.Goal]; !ok {
		return errInvalid("goal", "Unknown goal.")
	}
	if m := p.MacroSplit; m != nil {
		if m.ProteinPct < 5 || m.FatPct < 5 || m.CarbPct < 5 || m.ProteinPct+m.FatPct+m.CarbPct != 100 {
			return errInvalid("macro_split", "Each macro share must be at least 5 and the sum must be 100.")
		}
	}
	return nil
}

func (s *Server) meResponse(r *http.Request, userID string) (meResponse, error) {
	ctx := r.Context()
	offset, err := utcOffset(r)
	if err != nil {
		return meResponse{}, err
	}
	u, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return meResponse{}, err
	}
	now := s.now()
	day, resets := quota.Day(now, offset)
	used, err := s.store.RecognitionQuotaUsed(ctx, userID, day)
	if err != nil {
		return meResponse{}, err
	}
	resp := meResponse{User: toUserJSON(u), RecognitionQuota: quotaJSON{Limit: s.recognitionQuota, Used: used, ResetsAt: resets}}

	p, err := s.store.GetProfile(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return resp, nil
	}
	if err != nil {
		return meResponse{}, err
	}
	resp.Profile = &profileJSON{
		Sex: p.Sex, BirthYear: p.BirthYear, HeightCm: p.HeightCm, WeightKg: p.WeightKg,
		ActivityLevel: p.ActivityLevel, Goal: p.Goal,
		MacroSplit: &macroSplitJSON{p.ProteinPct, p.FatPct, p.CarbPct},
	}
	if d, ok := target.Calculate(target.Profile{
		Sex: p.Sex, Age: now.Year() - p.BirthYear, HeightCm: p.HeightCm, WeightKg: p.WeightKg,
		ActivityLevel: p.ActivityLevel, Goal: p.Goal, ProteinPct: p.ProteinPct, FatPct: p.FatPct, CarbPct: p.CarbPct,
	}); ok {
		resp.DailyTarget = &d
	}
	return resp, nil
}

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) error {
	resp, err := s.meResponse(r, principal(r.Context()).UserID)
	if errors.Is(err, store.ErrNotFound) {
		return errUnauthenticated
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) error {
	var p profileJSON
	if err := decodeJSON(w, r, &p); err != nil {
		return err
	}
	now := s.now()
	if err := p.validate(now); err != nil {
		return err
	}
	m := defaultSplit
	if p.MacroSplit != nil {
		m = *p.MacroSplit
	}
	userID := principal(r.Context()).UserID
	if err := s.store.PutProfile(r.Context(), userID, store.Profile{
		Sex: p.Sex, BirthYear: p.BirthYear, HeightCm: p.HeightCm, WeightKg: p.WeightKg,
		ActivityLevel: p.ActivityLevel, Goal: p.Goal, ProteinPct: m.ProteinPct, FatPct: m.FatPct, CarbPct: m.CarbPct,
	}, now); err != nil {
		return err
	}
	return s.getMe(w, r)
}

func (s *Server) deleteMe(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		AppleAuthorizationCode string `json:"apple_authorization_code"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body); err != nil {
			return err
		}
	}
	ctx := r.Context()
	userID := principal(ctx).UserID
	u, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.Provider == "apple" {
		err := s.auth.RevokeApple(ctx, userID, body.AppleAuthorizationCode)
		if errors.Is(err, auth.ErrAppleReauthRequired) {
			return &apiError{Status: http.StatusConflict, Code: "apple_reauth_required", Message: "Sign in with Apple again to delete the account."}
		}
		if err != nil {
			return err
		}
	}
	keys, err := s.store.DeleteUser(ctx, userID, s.now())
	if err != nil {
		return err
	}
	// Best effort now; the cleanup job retries the queue.
	for _, k := range keys {
		derr := s.photos.Delete(ctx, k)
		if ferr := s.store.FinishPhotoDeletion(ctx, k, derr); ferr != nil {
			s.log.ErrorContext(ctx, "photo deletion queue", "err", ferr)
		}
	}
	s.log.InfoContext(ctx, "account deleted", "user_id", userID, "photos", len(keys))
	w.WriteHeader(http.StatusNoContent)
	return nil
}
