# Split dishes into ingredients and cooking oil

Status: Accepted

USDA FoodData Central has few cooked Chinese dishes but covers raw ingredients well, and hidden oil is a large calorie error source. Thus the model returns each dish as a list of ingredients with grams, plus an estimate of cooking oil as its own ingredient (for example, tomato and egg stir-fry: tomato 150 g, egg 100 g, vegetable oil 10 g). The backend gets nutrients for each ingredient from the database (see [0002](0002-nutrients-from-database.md)). The app shows the dish name, and the user can open the dish to change its ingredients.

Trade-offs: the model output is longer and the edit UI has one more level. Do not replace the oil estimate with a fixed per-dish value without data from user changes.
