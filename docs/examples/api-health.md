# Example: API Health Validation Input

## Feature Description

API health endpoint returns `200` and `{ "ok": true }`.

## Expected API Task Payload Shape

```json
{
  "http_requests": [
    {
      "method": "GET",
      "url": "http://localhost:8080/health",
      "expect_status": 200,
      "expect_body_contains": "\"ok\":true"
    }
  ]
}
```

## Expected Evidence

- `api-transcript.json` in task artifact directory
- report manifest entry pointing to transcript

