package http

import (
	"bytes"
	"io"
	"net/http"
	"net/url"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

type reusableRequestBody struct {
	buffered      []byte
	stream        io.ReadCloser
	contentLength int64
	bufferedMode  bool
}

func prepareReusableBody(req *http.Request, maxAttempts int, recorder *traffic.Recorder) (*reusableRequestBody, error) {
	if req == nil || req.Body == nil {
		return &reusableRequestBody{}, nil
	}
	if maxAttempts <= 1 {
		return &reusableRequestBody{stream: req.Body, contentLength: req.ContentLength}, nil
	}
	defer req.Body.Close()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	trafficRecorder := httpRecorderOrAggregate(recorder)
	trafficRecorder.Add(int64(len(body)), 0)
	trafficRecorder.Flush()
	return &reusableRequestBody{buffered: body, contentLength: int64(len(body)), bufferedMode: true}, nil
}

func (b *reusableRequestBody) Open() (io.ReadCloser, int64, func() (io.ReadCloser, error)) {
	if b == nil {
		return nil, 0, nil
	}
	if b.buffered != nil {
		getBody := func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(b.buffered)), nil
		}
		rc, _ := getBody()
		return rc, int64(len(b.buffered)), getBody
	}
	if b.stream != nil {
		stream := b.stream
		b.stream = nil
		return stream, b.contentLength, nil
	}
	return nil, 0, nil
}

func (b *reusableRequestBody) Close() error {
	if b == nil || b.stream == nil {
		return nil
	}
	err := b.stream.Close()
	b.stream = nil
	return err
}

func cloneProxyRequest(req *http.Request, body *reusableRequestBody, candidate httpCandidate, rule model.HTTPRule, frontendPath string, recorder *traffic.Recorder) (*http.Request, error) {
	incomingHost := req.Host
	incomingScheme := requestScheme(req)
	out := req.Clone(req.Context())
	targetURL := cloneURL(candidate.target)
	dialAddress := candidate.dialAddress
	if redirectTarget, ok := parseInternalRedirectTarget(req.URL.Path, frontendPath); ok {
		targetURL = redirectTarget
		targetURL.RawQuery = req.URL.RawQuery
		dialAddress = addressWithDefaultPort(targetURL)
	} else {
		targetURL.Path = rewriteRequestPath(req.URL.Path, frontendPath, normalizeURLPath(candidate.target.Path))
		targetURL.RawPath = ""
		targetURL.RawQuery = req.URL.RawQuery
	}
	out.URL = targetURL
	out.URL.RawQuery = req.URL.RawQuery
	out.URL.Fragment = req.URL.Fragment
	out.URL.ForceQuery = req.URL.ForceQuery
	out.RequestURI = ""
	out.Host = targetURL.Host
	out = out.WithContext(withDialAddress(out.Context(), dialAddress))
	if ruleUsesRelay(rule) {
		holder := &selectedRelayAddressHolder{}
		ctx := withSelectedRelayAddressHolder(out.Context(), holder)
		ctx = withSelectedRelayConnTrace(ctx, holder)
		out = out.WithContext(ctx)
	}
	if body != nil {
		out.Body, out.ContentLength, out.GetBody = body.Open()
		if out.Body != nil {
			out.Body = newTrafficReadCloser(out.Body, recorder, !body.bufferedMode)
			if out.GetBody != nil {
				getBody := out.GetBody
				out.GetBody = func() (io.ReadCloser, error) {
					body, err := getBody()
					if err != nil {
						return nil, err
					}
					return newTrafficReadCloser(body, recorder, false), nil
				}
			}
		}
	} else {
		out.Body = nil
		out.ContentLength = 0
	}
	if overrides := HeaderOverridesFromRule(rule, req, incomingHost, incomingScheme); len(overrides) > 0 {
		ApplyHeaderOverrides(out, overrides)
	}
	return out, nil
}

type trafficReadCloser struct {
	io.ReadCloser
	recorder      *traffic.Recorder
	recordInbound bool
}

func newTrafficReadCloser(delegate io.ReadCloser, recorder *traffic.Recorder, recordInbound bool) io.ReadCloser {
	return &trafficReadCloser{
		ReadCloser:    delegate,
		recorder:      httpRecorderOrAggregate(recorder),
		recordInbound: recordInbound,
	}
}

func httpRecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder {
	if recorder != nil {
		return recorder
	}
	return traffic.NewHTTPRecorder()
}

func (c trafficReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if c.recordInbound && n > 0 {
		c.recorder.Add(int64(n), 0)
	}
	if err != nil {
		c.recorder.Flush()
	}
	return n, err
}

func (c trafficReadCloser) Close() error {
	c.recorder.Flush()
	return c.ReadCloser.Close()
}

func cloneURL(src *url.URL) *url.URL {
	if src == nil {
		return &url.URL{}
	}
	copyValue := *src
	return &copyValue
}
