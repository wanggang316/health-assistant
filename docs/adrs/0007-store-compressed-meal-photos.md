# Store compressed meal photos

Status: Accepted

Users want to see their past meals, and stored photos with the user's changes are the data to evaluate and improve recognition. Thus the backend stores a compressed copy of each meal photo (longest side 1280 px) in object storage, not the original. When a user deletes a meal entry or the account, the backend deletes its photos.

Trade-offs: meal photos are personal data and need storage cost and a deletion path. Do not keep photos after the related meal entry or user is deleted.
