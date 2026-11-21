# Health advice is derived data and is made again after changes

Status: Accepted

Meal entries can change or be deleted at any time after confirmation. Thus meal feedback and daily summaries are derived data: the backend caches them and marks them stale when a related meal entry or the profile changes. A daily summary is made when the user opens a day that has at least one meal entry and no fresh summary. The text model for advice is `gemini-3.1-flash-lite`. The first version has no push notifications.

When a user changes a meal entry that came from a recognition, the backend keeps both the original recognition and the final entry, because these pairs are the data to evaluate the model.

Trade-offs: the first open of a day waits for the text model. Do not show a stale summary as current.
