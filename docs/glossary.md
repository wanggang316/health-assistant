# Health Assistant

Health Assistant is an iOS app that records what a user eats from meal photos, estimates nutrition, and gives diet advice. This glossary defines the terms that the app, the backend, and the documents share.

## Accounts

**User**:
A person who has an account in the app. One user has exactly one sign-in identity.
_Avoid_: Customer, member

**Sign-in Identity**:
An Apple or Google account that identifies one user. Two identities with the same email are two different users.
_Avoid_: Login, provider account

**Profile**:
The body data and goal of a user (height, weight, sex, age, activity level, goal) that the app uses to calculate the daily targets.
_Avoid_: Settings, user info

**Daily Target**:
The calorie and macro amounts that a user should eat in one day, calculated from the profile.
_Avoid_: Budget, quota

## Meals

**Meal Photo**:
A photo of food that a user takes or selects to start a recognition. The backend stores a compressed copy.
_Avoid_: Image, picture

**Recognition**:
The result that the model returns for one meal photo: food items with estimated grams and cooking method. A recognition is not a record and does not count in any total.
_Avoid_: Analysis, detection, draft entry

**Meal Entry**:
One meal that a user confirmed, from a recognition or by manual input. Only meal entries count in daily totals.
_Avoid_: Record, log, meal

**Food Item**:
One dish or food in a meal entry or a recognition, with a name and one or more ingredients. Its grams and nutrients are the sum of its ingredients.
_Avoid_: Dish (in code), product

**Ingredient**:
One food inside a dish in a recognition or a meal entry, with grams. Cooking oil is an ingredient. Nutrients come from the nutrition database per ingredient.
_Avoid_: Component, part

**Meal Type**:
Breakfast, lunch, dinner, or snack. The app sets it from the local eaten time, and the user can change it.
_Avoid_: Meal slot, category

**Entry Date**:
The local date of a meal entry, from its eaten time and the device UTC offset at that time. Daily totals group by entry date.
_Avoid_: Day, log date

**Recognition Quota**:
The maximum number of recognitions that one user can request in one day. Manual input has no quota.
_Avoid_: Rate limit, credits

## Advice

**Meal Feedback**:
A short comment on one meal entry, made right after the user confirms it.
_Avoid_: Tip, suggestion

**Daily Summary**:
A comment on all meal entries of one day compared with the daily target.
_Avoid_: Report, review

**Health Advice**:
The common name for meal feedback and daily summaries. Health advice is general diet information, not medical advice.
_Avoid_: Diagnosis, recommendation
