package metrics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// base is embedded in every service.
type base struct{ c *Client }

func (b base) get[T any](ctx context.Context, path string, q url.Values) (T, *Meta, error) {
	return b.c.Do[T](ctx, Request{Path: path, Query: q})
}

// HealthService answers whether the process is up and whether it can work.
// Neither endpoint is behind the token.
type HealthService struct{ base }

// Live is liveness: the process is up and serving this listener.
//
// GET /healthz.
func (s *HealthService) Live(ctx context.Context) (Health, *Meta, error) {
	return s.get[Health](ctx, "healthz", nil)
}

// Ready is readiness: the database answers and every NATS connection is up.
// A process that is not ready answers 503 with the reason, which comes back
// here as the payload with a nil error — being not ready is an answer.
//
// GET /readyz.
func (s *HealthService) Ready(ctx context.Context) (Readiness, *Meta, error) {
	return s.c.Do[Readiness](ctx, Request{Path: "readyz", Answers: []int{http.StatusServiceUnavailable}})
}

// SeriesService reads what the process measures.
type SeriesService struct{ base }

// Info describes the listener: its configuration, its catalog of series and
// its dashboard layout. The catalog grows as things first happen — a
// counter still at zero publishes nothing.
//
// GET /api/meta.
func (s *SeriesService) Info(ctx context.Context) (Info, *Meta, error) {
	return s.get[Info](ctx, "api/meta", nil)
}

// Live returns the latest reading of every series.
//
// GET /api/live.
func (s *SeriesService) Live(ctx context.Context) (Snapshot, *Meta, error) {
	return s.get[Snapshot](ctx, "api/live", nil)
}

// Query answers a range request, from memory when memory reaches back far
// enough and from Postgres otherwise.
//
// GET /api/query.
func (s *SeriesService) Query(ctx context.Context, req QueryRequest) (Answer, *Meta, error) {
	return s.get[Answer](ctx, "api/query", req.query())
}

// Stream opens the live stream: one snapshot per scrape, the first at once.
// Read it with Next and close it when done; cancelling ctx closes it too.
//
// GET /api/stream.
func (s *SeriesService) Stream(ctx context.Context) (*Stream, *Meta, error) {
	body, meta, err := s.c.DoRaw(ctx, Request{Path: "api/stream", Accept: "text/event-stream", NoRetry: true})
	if err != nil {
		return nil, meta, err
	}
	return &Stream{body: body, r: bufio.NewReaderSize(body, 64<<10)}, meta, nil
}

// Stream is an open server-sent event stream of snapshots.
type Stream struct {
	body io.ReadCloser
	r    *bufio.Reader
}

// ErrStreamClosed is returned by Next once the listener has ended the
// stream — a restart, usually. Open another.
var ErrStreamClosed = errors.New("metrics: the stream ended")

// Next blocks until the next snapshot arrives.
func (s *Stream) Next() (Snapshot, error) {
	var data strings.Builder
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return Snapshot{}, ErrStreamClosed
			}
			return Snapshot{}, fmt.Errorf("metrics: reading the stream: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			// A blank line ends an event; one without data is a
			// keep-alive.
			if data.Len() == 0 {
				continue
			}
			var snap Snapshot
			if err := json.Unmarshal([]byte(data.String()), &snap); err != nil {
				return Snapshot{}, fmt.Errorf("metrics: decoding the stream: %w", err)
			}
			return snap, nil
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		// Comments, event names and ids carry nothing this reads.
	}
}

// Close ends the stream.
func (s *Stream) Close() error { return s.body.Close() }

// PrometheusService serves the text exposition, for a scraper — or a person
// who wants every current value in one read.
type PrometheusService struct{ base }

// Scrape reads the exposition and parses it.
//
// GET /metrics.
func (s *PrometheusService) Scrape(ctx context.Context) (Exposition, *Meta, error) {
	body, meta, err := s.c.DoRaw(ctx, Request{Path: "metrics", Accept: "text/plain"})
	if err != nil {
		return Exposition{}, meta, err
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, maxBody))
	if err != nil {
		return Exposition{}, meta, fmt.Errorf("metrics: reading the exposition: %w", err)
	}
	exp, err := ParseExposition(string(raw))
	return exp, meta, err
}

// ParseExposition reads the Prometheus text format: # HELP and # TYPE
// lines, and sample lines of name{labels} value [timestamp].
func ParseExposition(text string) (Exposition, error) {
	exp := Exposition{Raw: text, Help: map[string]string{}, Types: map[string]string{}}
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "#"); ok {
			fields := strings.Fields(rest)
			if len(fields) >= 2 && fields[0] == "HELP" {
				exp.Help[fields[1]] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest)[4:], " "+fields[1]))
			}
			if len(fields) >= 3 && fields[0] == "TYPE" {
				exp.Types[fields[1]] = fields[2]
			}
			continue
		}
		sample, err := parseSample(line)
		if err != nil {
			return exp, fmt.Errorf("metrics: exposition line %d: %w", n+1, err)
		}
		exp.Samples = append(exp.Samples, sample)
	}
	return exp, nil
}

func parseSample(line string) (Sample, error) {
	s := Sample{}
	rest := line
	if i := strings.IndexAny(line, "{ "); i >= 0 && line[i] == '{' {
		s.Name = line[:i]
		labels, after, err := parseLabels(line[i+1:])
		if err != nil {
			return s, err
		}
		s.Labels, rest = labels, after
	} else {
		name, after, _ := strings.Cut(line, " ")
		s.Name, rest = name, after
	}
	fields := strings.Fields(rest)
	if s.Name == "" || len(fields) == 0 {
		return s, fmt.Errorf("%q is not a sample", line)
	}
	switch fields[0] {
	case "NaN":
		s.Value = math.NaN()
	case "+Inf":
		s.Value = math.Inf(1)
	case "-Inf":
		s.Value = math.Inf(-1)
	default:
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return s, fmt.Errorf("%q has no number", line)
		}
		s.Value = v
	}
	return s, nil
}

// parseLabels reads a="x",b="y"} and returns what follows the brace.
func parseLabels(text string) (map[string]string, string, error) {
	labels := map[string]string{}
	for {
		text = strings.TrimLeft(text, " ,")
		if strings.HasPrefix(text, "}") {
			return labels, text[1:], nil
		}
		name, rest, ok := strings.Cut(text, "=")
		if !ok || !strings.HasPrefix(rest, `"`) {
			return nil, "", fmt.Errorf("malformed labels in %q", text)
		}
		// Find the closing quote, honoring escapes.
		end := 1
		for end < len(rest) && rest[end] != '"' {
			if rest[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(rest) {
			return nil, "", fmt.Errorf("an unterminated label value in %q", text)
		}
		value, err := strconv.Unquote(rest[:end+1])
		if err != nil {
			return nil, "", fmt.Errorf("a malformed label value in %q", text)
		}
		labels[strings.TrimSpace(name)] = value
		text = rest[end+1:]
	}
}
