# UI design (v1)

Status: Draft for review. The interactive prototype is [prototype/index.html](prototype/index.html). Open it from a local HTTP server, for example `python3 -m http.server -d docs/design/prototype`.

## Concept

The app is about Chinese home meals, so the visual language comes from the table: blue-and-white porcelain, celadon, tea green, and egg yolk. The one memorable element is the **day bowl**: a porcelain bowl, seen from above, that fills as the user confirms meal entries. The other parts of the UI stay quiet.

- The filled area of the bowl is proportional to eaten energy divided by the daily target.
- The fill is split into three sectors by the share of energy from protein, fat, and carbohydrate.
- When the eaten energy is more than the daily target, the bowl gets a chili-red ring.
- The trend screen shows one small bowl per day, so the same shape means the same thing everywhere.

## Tokens

| Token | Value | Use |
|---|---|---|
| `rice` | `#F5F7F2` | Screen background. It matches the paper color of the illustrations. |
| `celadon` | `#E3EEDD` | Quiet panels: summaries, stepper buttons, meal type chip. |
| `tea` | `#2F7D5B` | Primary actions and protein. |
| `yolk` | `#F2B33D` | Fat, and warnings that ask the user to check a value. |
| `porcelain` | `#3E6FB0` | Carbohydrate, and the bowl pattern. |
| `chili` | `#D9513C` | Over target and destructive actions only. |
| `ink` | `#1E2A24` | Text. Secondary text `#56635C`, tertiary `#8B968F`. |

Type: SF Pro and PingFang SC for text; SF Pro Rounded for all numbers (`.fontDesign(.rounded)` with `monospacedDigit()`). Titles 30 pt bold, body 15–17 pt, captions 13 pt.

Shape: cards 18–24 pt corner radius, buttons 16 pt, sheets 30 pt. Cards have no shadow; only floating elements (tab bar, toast, sheet) have one.

## Screens

| Screen | Purpose | Key behavior |
|---|---|---|
| Sign in | Apple or Google sign-in. | Hero illustration; legal line says advice is not medical advice. |
| Onboarding | Profile in three steps: body data, activity level, goal. | The last step counts up to the daily target and shows the macro split. "Skip for now" goes to Today without targets. |
| Today | Day bowl, macro bars, daily summary, meal entries by meal type. | Empty meal types show an illustration and open the camera. |
| Camera | Take or pick a meal photo. | Shows the remaining recognition quota. Manual input does not use quota. |
| Recognizing | Wait for the recognition. | A yolk scan line moves over the photo; one pin appears per dish found. |
| Confirm sheet | Review the recognition before it becomes a meal entry (ADR 0003). | Dishes expand to ingredients with steppers; cooking oil has its own tag (ADR 0009); the cross-check warning asks the user to check grams (ADR 0002). |
| Meal entry detail | Photo, dishes, meal feedback, delete. | |
| Trend | Week of small bowls, averages, macro bars with target marks, weekly summary. | |
| Me | Targets, profile, language, privacy policy, sign out, delete account. | Account deletion sheet lists what is deleted. |
| Manual input | Search foods and enter grams. | Stub in the prototype. |

## Motion

Motion answers a user action. Do not add idle animation, except the slow float of the sign-in illustration.

| Moment | Prototype | SwiftUI |
|---|---|---|
| Push and pop | Slide 0.45 s, ease out | `NavigationStack` default |
| Camera | Slides up from the bottom | `fullScreenCover` |
| Shutter | White flash 0.45 s | Overlay opacity animation |
| Recognizing | Scan line loop; dish pins pop in with a spring, 380 ms apart | `PhaseAnimator` for the scan line; `.spring(duration: 0.5, bounce: 0.3)` per pin |
| Confirm sheet | Springs up; dish cards come in 80 ms apart | `.sheet` with `presentationDetents([.large])`; staggered `.transition(.move(edge: .bottom).combined(with: .opacity))` |
| Confirm | Dish chips fly into the bowl, then the bowl fills with a small overshoot and the numbers count up | `matchedGeometryEffect` for chips; `Animatable` bowl shape with `.spring(bounce: 0.25)`; `.contentTransition(.numericText())` |
| Meal feedback | Toast drops from the top for about 4 s | Overlay with `.transition(.move(edge: .top))` |
| Daily summary | Shimmer while the text is made | `redacted(reason: .placeholder)` plus a shimmer modifier |

All motion must respect Reduce Motion: replace movement with a cross-fade and show final values at once.

## Illustrations

Generated with one prompt style (flat editorial, soft grain, the token colors, off-white `#F5F7F2` background). Files are in `prototype/assets/`. Before release, make final versions at @2x and @3x with transparent backgrounds, or as vector art.

| File | Use |
|---|---|
| `hero.jpg` | Sign in |
| `empty.jpg` | Empty meal type on Today |
| `scan.jpg` | Manual input and loading states |
| `meal-photo.jpg` | Sample photo for the prototype only. Do not ship it. |

## Copy rules

- Simplified Chinese first; English through the String Catalog.
- Name actions by what they do: "确认并记录", "删除这条记录", "删除账户".
- Warnings say what to check, not what failed inside the system.
- Every nutrition number is an estimate; the Me screen states the usual error range (20–40%).

## Open points

- App name: "Gump Health Assistant" is a working name.
- Dark mode is not designed yet.
- The manual input flow and the profile edit screens are stubs.
