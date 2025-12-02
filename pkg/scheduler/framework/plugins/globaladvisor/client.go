package globaladvisor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// ScoreClient 调用 Java GS 的简单 HTTP 客户端
type ScoreClient struct {
	baseURL    string
	httpClient *http.Client
	retry      int
	backoff    time.Duration
}

// NewScoreClient 创建客户端
func NewScoreClient(baseURL string, timeout time.Duration, retry int, backoff time.Duration) *ScoreClient {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &ScoreClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
		retry:   retry,
		backoff: backoff,
	}
}

// GetScore 请求单个集群的评分
// targetCluster: 可选参数，指定希望亲和的目标集群
func (c *ScoreClient) GetScore(ctx context.Context, clusterName string, targetCluster string) (*ClusterScore, error) {
	// 构造 URL，如果有 targetCluster，追加参数
	url := fmt.Sprintf("%s/api/advisor/score?cluster=%s", c.baseURL, clusterName)
	if targetCluster != "" {
		url = fmt.Sprintf("%s&target=%s", url, targetCluster)
	}

	var lastErr error
	for attempt := 0; attempt <= c.retry; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.backoff):
				continue
			}
		} else {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(c.backoff):
					continue
				}
			}
			var sc ClusterScore
			if err := json.Unmarshal(body, &sc); err != nil {
				return nil, err
			}
			return &sc, nil
		}
	}
	return nil, lastErr
}
