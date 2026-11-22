# The entry date is the local date when the meal was eaten

Status: Accepted

Users travel across time zones, and a meal must stay on the day the user saw it. Thus each meal entry stores the eaten time in UTC together with the device UTC offset at that time, and its entry date is the local date from that offset. Daily totals and daily summaries group by entry date, not by server time or the current device time zone. The meal type (breakfast, lunch, dinner, snack) is set from the local eaten time, and the user can change it.

Trade-offs: two meals at the same UTC time can fall on different entry dates. Do not recalculate old entry dates when the device time zone changes.
