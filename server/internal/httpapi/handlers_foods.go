package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/wanggang316/health-assistant/server/internal/nutrition"
)

type foodJSON struct {
	FDCID       int64               `json:"fdc_id"`
	Description string              `json:"description"`
	DataType    string              `json:"data_type"`
	Per100g     nutrition.Nutrients `json:"per_100g"`
}

func toFoodJSON(f nutrition.Food) foodJSON {
	return foodJSON{FDCID: f.FDCID, Description: f.Description, DataType: f.DataType, Per100g: f.Per100g.Rounded()}
}

func (s *Server) searchFoods(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len([]rune(q)) > 100 {
		return errInvalid("q", "q must have 1 to 100 characters.")
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 25 {
			return errInvalid("limit", "limit must be from 1 to 25.")
		}
		limit = n
	}
	foods, err := s.nutrition.Search(r.Context(), q, limit)
	if err != nil {
		return err
	}
	out := make([]foodJSON, 0, len(foods))
	for _, f := range foods {
		out = append(out, toFoodJSON(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{"foods": out})
	return nil
}
