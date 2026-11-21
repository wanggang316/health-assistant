# Rules calculate health advice, the model only writes the text

Status: Accepted

Health advice must be correct in its numbers and must stay general diet information. Thus the backend calculates the facts with rules (for example, the difference between eaten calories and macros and the daily target), and a language model only writes those facts as readable text. The app states that health advice is not medical advice, and the advice does not give guidance for diseases.

Trade-offs: the text is less flexible than free model output. Do not let the model calculate the numbers or add medical claims. Reconsider only together with a review of the medical and legal scope.
