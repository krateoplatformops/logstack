package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/krateoplatformops/plumbing/http/response"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func Logs(etcdClient *clientv3.Client) http.Handler {
	res := &logsHandler{}
	res.etcdClient = etcdClient
	return res
}

var _ http.Handler = (*logsHandler)(nil)

const (
	cacheTTL       = 10 * time.Second
	maxLastMinutes = 1440
	maxLimit       = 1000
)

type logEntry struct {
	Timestamp time.Time
	Raw       []byte
}

type cachedResponse struct {
	timestamp time.Time
	data      []byte
}

type logsHandler struct {
	etcdClient *clientv3.Client
	cache      sync.Map // map[string]cachedResponse
}

func (r *logsHandler) ServeHTTP(wri http.ResponseWriter, req *http.Request) {
	traceId, lastMinutes, limit, offset, err := parseParams(req)
	if err != nil {
		response.BadRequest(wri, err)
		return
	}

	cacheKey := fmt.Sprintf("%s-%d-%d-%d", traceId, lastMinutes, limit, offset)
	if cached, ok := r.cache.Load(cacheKey); ok {
		cr := cached.(cachedResponse)
		if time.Since(cr.timestamp) < cacheTTL {
			wri.Header().Set("Content-Type", "application/x-ndjson")
			wri.Write(cr.data)
			return
		}
		r.cache.Delete(cacheKey)
	}

	cutoffTime := time.Now().Add(-time.Duration(lastMinutes) * time.Minute)
	prefix := fmt.Sprintf("logs/%s/", traceId)

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	resp, err := r.etcdClient.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		response.InternalError(wri, fmt.Errorf("failed to query etcd: %w", err))
		return
	}

	var entries []logEntry
	for _, kv := range resp.Kvs {
		t, err := parseTimestampFromValue(kv.Value)
		if err != nil {
			continue
		}
		if t.After(cutoffTime) {
			entries = append(entries, logEntry{Timestamp: t, Raw: kv.Value})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	total := len(entries)
	if offset >= total {
		entries = []logEntry{}
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		entries = entries[offset:end]
	}

	// Set header Link for pagination RFC 8288
	baseURL := req.URL.Path
	query := req.URL.Query()

	var links []string

	// Self link
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	query.Set("traceId", traceId)
	query.Set("lastMinutes", strconv.Itoa(lastMinutes))
	selfLink := fmt.Sprintf("<%s?%s>; rel=\"self\"", baseURL, query.Encode())
	links = append(links, selfLink)

	// Next link
	nextOffset := offset + limit
	if nextOffset < total {
		query.Set("offset", strconv.Itoa(nextOffset))
		nextLink := fmt.Sprintf("<%s?%s>; rel=\"next\"", baseURL, query.Encode())
		links = append(links, nextLink)
	}

	// Prev link
	prevOffset := max(offset-limit, 0)
	if offset > 0 {
		query.Set("offset", strconv.Itoa(prevOffset))
		prevLink := fmt.Sprintf("<%s?%s>; rel=\"prev\"", baseURL, query.Encode())
		links = append(links, prevLink)
	}

	wri.Header().Set("Content-Type", "application/x-ndjson")
	if len(links) > 0 {
		wri.Header().Set("Link", strings.Join(links, ", "))
	}

	var output []byte
	for _, e := range entries {
		wri.Write(e.Raw)
		wri.Write([]byte("\n"))
		output = append(output, e.Raw...)
		output = append(output, '\n')
	}

	r.cache.Store(cacheKey, cachedResponse{
		timestamp: time.Now(),
		data:      output,
	})
}

func parseParams(r *http.Request) (traceId string, lastMinutes int, limit int, offset int, err error) {
	traceId = r.URL.Query().Get("traceId")
	if traceId == "" {
		err = fmt.Errorf("traceId parameter is required")
		return
	}

	lastMinutes = 40
	if lm := r.URL.Query().Get("lastMinutes"); lm != "" {
		v, e := strconv.Atoi(lm)
		if e != nil || v <= 0 || v > maxLastMinutes {
			err = fmt.Errorf("lastMinutes must be a positive integer up to %d", maxLastMinutes)
			return
		}
		lastMinutes = v
	}

	limit = 100
	if l := r.URL.Query().Get("limit"); l != "" {
		v, e := strconv.Atoi(l)
		if e != nil || v <= 0 || v > maxLimit {
			err = fmt.Errorf("limit must be a positive integer up to %d", maxLimit)
			return
		}
		limit = v
	}

	offset = 0
	if o := r.URL.Query().Get("offset"); o != "" {
		v, e := strconv.Atoi(o)
		if e != nil || v < 0 {
			err = fmt.Errorf("offset must be a non-negative integer")
			return
		}
		offset = v
	}
	return
}

func parseTimestampFromValue(value []byte) (time.Time, error) {
	var obj map[string]any
	if err := json.Unmarshal(value, &obj); err != nil {
		return time.Time{}, err
	}

	tsStr, ok := obj["time"].(string)
	if !ok {
		return time.Time{}, fmt.Errorf("time field missing or not string")
	}

	return time.Parse(time.RFC3339, tsStr)
}
