# One user per sign-in identity, no merge by email

Status: Accepted

Users sign in only with Apple or Google. Sign in with Apple can hide the real email, and an email match does not prove that two identities belong to the same person. Thus each sign-in identity creates its own user, and the backend never merges users by email. The backend keys a user on the provider and the provider's stable subject ID, not on the email.

Trade-offs: a person who signs in once with Apple and once with Google gets two users with separate data. If this becomes a problem, add an explicit "link another sign-in" action in settings that needs the user to be signed in with both identities. Account deletion (an App Store rule) deletes the user, the meal entries, and the stored meal photos.
