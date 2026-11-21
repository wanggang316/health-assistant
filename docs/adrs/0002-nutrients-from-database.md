# Nutrients come from a database, not from the model

Status: Accepted

Vision models are good at naming foods but bad at numbers: grams are the largest error source, and micronutrients from a model are mostly invented. Thus the model returns only food items, estimated grams, and cooking method. The backend calculates nutrients from a nutrition database (USDA FoodData Central first, CC0). The calories that the model estimates are used only as a cross-check: when they differ from the database value by more than about 35%, the app asks the user to check the grams.

Trade-offs: the backend must match food names to database entries, and this is difficult for Chinese dishes that USDA does not cover well. Do not add the China Food Composition Table without a commercial license. Do not use Open Food Facts data except for barcodes, because its ODbL license is share-alike. Reconsider if a licensed database with good Chinese dish coverage becomes available.
