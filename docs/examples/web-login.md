# Example: Web Login Validation Input

## Feature Description

User can log into the web app with valid credentials and sees the dashboard.

## Expected Planner Output Shape

- `feature_spec.acceptance_criteria`: non-empty list
- `test_plan.journeys`: non-empty list
- `tasks`: at least one `web` task with `P0..P3` priority and `dedupe_key`

## Expected Final Verdict Shape

- `status`: `pass` | `fail` | `cannot_verify`
- `reasons`: non-empty list
- `coverage`: criterion IDs mapped to evidence refs

