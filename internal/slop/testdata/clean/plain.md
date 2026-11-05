# Backup schedule

Backups run nightly at 02:00 and are kept for 14 days. A failed run sends one
alert and is retried at 03:00.

Restores are tested on the first Monday of each month. The last test took
11 minutes for a 40 GB dataset.
