package globaladvisor

import (
	"context"
	"time"

	clusterv1alpha1 "github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1"
	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/scheduler/framework"
	"k8s.io/klog/v2"
)

const (
	Name           = "GlobalAdvisor"
	defaultGSURL   = "http://127.0.0.1:8088"
	defaultTimeout = 300 * time.Millisecond
	defaultRetry   = 1
	defaultBackoff = 100 * time.Millisecond
	defaultTTL     = 3 * time.Second
	defaultScore   = 50.0
)

// GlobalAdvisor 插件结构体
type GlobalAdvisor struct {
	scoreClient  *ScoreClient
	cache        *simpleCache
	defaultScore float64
}

// 确保实现 ScorePlugin 接口
var _ framework.ScorePlugin = &GlobalAdvisor{}

// New 创建插件实例（registry 调用）
func New() (framework.Plugin, error) {
	// 使用默认配置，后续可以通过配置系统扩展
	gsURL := defaultGSURL
	timeout := defaultTimeout
	retry := defaultRetry
	backoff := defaultBackoff
	ttl := defaultTTL

	client := NewScoreClient(gsURL, timeout, retry, backoff)
	c := newSimpleCache(ttl)

	return &GlobalAdvisor{
		scoreClient:  client,
		cache:        c,
		defaultScore: defaultScore,
	}, nil
}

// Name 返回插件名
func (g *GlobalAdvisor) Name() string {
	return Name
}

// Score 实现 ScorePlugin 的 Score 方法
func (g *GlobalAdvisor) Score(ctx context.Context, _ *workv1alpha2.ResourceBindingSpec, cluster *clusterv1alpha1.Cluster) (int64, *framework.Result) {
	clusterName := cluster.Name
	klog.V(3).Infof("[GlobalAdvisor] Score called for cluster=%s", clusterName)

	// 1) cache check
	if s, ok := g.cache.Get(clusterName); ok {
		klog.V(4).Infof("[GlobalAdvisor] cache hit cluster=%s score=%.2f", clusterName, s)
		return int64(s), framework.NewResult(framework.Success)
	}

	// 2) call Java GS
	// derive a short timeout from ctx
	ctxTimeout, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	scoreResp, err := g.scoreClient.GetScore(ctxTimeout, clusterName)
	if err != nil {
		klog.Warningf("[GlobalAdvisor] failed to get score for cluster=%s: %v; fallback to default score %.2f", clusterName, err, g.defaultScore)
		// fallback: use default score
		return int64(g.defaultScore), framework.NewResult(framework.Success)
	}

	score := scoreResp.HealthScore
	// clamp
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	// cache
	g.cache.Set(clusterName, score)

	klog.Infof("[GlobalAdvisor] got score cluster=%s score=%.2f reason=%s", clusterName, score, scoreResp.Reason)
	return int64(score), framework.NewResult(framework.Success)
}

// ScoreExtensions 返回 ScoreExtensions 接口
func (g *GlobalAdvisor) ScoreExtensions() framework.ScoreExtensions {
	return g
}

// NormalizeScore 标准化分数
func (g *GlobalAdvisor) NormalizeScore(_ context.Context, _ framework.ClusterScoreList) *framework.Result {
	// 分数已经在 0-100 范围内，不需要标准化
	return framework.NewResult(framework.Success)
}
