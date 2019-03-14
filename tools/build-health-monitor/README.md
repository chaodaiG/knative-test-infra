## build-health-monitor
build-health-monitor is a tool downloading test results from Testgrid,
analyze all runs from all repos, and reports if there is any job has
recent failures, or if start time of latest run being very old, i.e.
3 times earlier than median interval

## Basis Usage
Directly run this tool