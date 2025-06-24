
# `auger` -  Snowplow Log Query Service

The proposed HTTP service exposes access to logs stored in etcd, enabling traceable and efficient log inspection. 

Key features:

* Trace-Based Querying
  Search logs by `traceId`, allowing correlation of distributed events across pods or services.

* Time Range Filtering
  Filter logs by relative time (e.g., logs from the last 40 minutes) using the `lastMinutes` query parameter.

* NDJSON Output
  Logs are returned as [**newline-delimited JSON (NDJSON)**](https://github.com/ndjson/ndjson-spec), making it easy to stream or parse line-by-line with tools like `jq`.

* Pagination Support
  Results are paginated with support for `limit` and `offset` parameters.
  Pagination metadata is exposed via the `Link` HTTP header following [[RFC 8288 (Web Linking)](https://www.rfc-editor.org/rfc/rfc8288)](https://www.rfc-editor.org/rfc/rfc8288).

* In-Memory Response Caching
  Responses are cached in memory for XX seconds to reduce pressure on etcd under high read load.

* Sorted Results
  Logs are sorted by timestamp in ascending order before pagination.

* Fast and Resilient
  Each etcd query is executed with a 5-second timeout to prevent hanging requests.