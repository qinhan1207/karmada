package globaladvisor

import (
	"context"
	"os"
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

	// 🔥 定义亲和性 Label Key
	AffinityLabelKey = "scheduler.qinhan.io/affinity-target"
)

// GlobalAdvisor 插件结构体
type GlobalAdvisor struct {
	scoreClient  *ScoreClient
	cache        *simpleCache
	defaultScore float64
}

// 确保实现 ScorePlugin 接口
var _ framework.ScorePlugin = &GlobalAdvisor{}

// New 创建插件实例
func New() (framework.Plugin, error) {
	// 1. 优先从环境变量获取 URL
	gsURL := os.Getenv("GLOBAL_SCHEDULER_URL")
	if gsURL == "" {
		gsURL = defaultGSURL
	}
	klog.Infof("[GlobalAdvisor] Connecting to Global Scheduler at: %s", gsURL)

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

func (g *GlobalAdvisor) Name() string {
	return Name
}

// Score 实现打分逻辑
func (g *GlobalAdvisor) Score(ctx context.Context, spec *workv1alpha2.ResourceBindingSpec, cluster *clusterv1alpha1.Cluster) (int64, *framework.Result) {
	clusterName := cluster.Name

	// 1. 🔥 解析亲和性目标 (Affinity Target)
	// 我们尝试从 Workload 的 Label 中获取用户指定的 affinity-target
	targetCluster := detectTargetCluster(spec)
	if targetCluster == "" {
		// === 实验专用逻辑 ===
		// 如果您想在实验中测试 "Web" 服务想亲和 "member2"
		// 可以在部署调度器时设置环境变量 TEST_AFFINITY_TARGET=member2
		if t := os.Getenv("TEST_AFFINITY_TARGET"); t != "" {
			targetCluster = t
		}
		// ===================
	}

	klog.V(3).Infof("[GlobalAdvisor] Score called for cluster=%s, target=%s", clusterName, targetCluster)

	// 2. 缓存检查 (注意：如果有 target，缓存 key 需要变化，或者干脆不缓存带 target 的请求)
	// 简单起见，如果设定了 target，我们跳过缓存，强制查询最新距离
	if targetCluster == "" {
		if s, ok := g.cache.Get(clusterName); ok {
			return int64(s), framework.NewResult(framework.Success)
		}
	}

	// 3. 调用 Java GS
	ctxTimeout, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// 传入 targetCluster
	scoreResp, err := g.scoreClient.GetScore(ctxTimeout, clusterName, targetCluster)
	if err != nil {
		klog.Warningf("[GlobalAdvisor] failed to get score for cluster=%s: %v; fallback", clusterName, err)
		return int64(g.defaultScore), framework.NewResult(framework.Success)
	}

	score := scoreResp.HealthScore
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	// 只有非亲和请求才写缓存
	if targetCluster == "" {
		g.cache.Set(clusterName, score)
	}

	klog.Infof("[GlobalAdvisor] got score cluster=%s score=%.2f reason=%s", clusterName, score, scoreResp.Reason)
	return int64(score), framework.NewResult(framework.Success)
}

func (g *GlobalAdvisor) ScoreExtensions() framework.ScoreExtensions {
	return g
}

func (g *GlobalAdvisor) NormalizeScore(_ context.Context, _ framework.ClusterScoreList) *framework.Result {
	return framework.NewResult(framework.Success)
}

// detectTargetCluster 会尝试从 BindingSpec 中解析用户配置的亲和目标。
// 目前支持从 ReplicaRequirements 以及 Components.*.ReplicaRequirements 的 NodeSelector 中读取。
func detectTargetCluster(spec *workv1alpha2.ResourceBindingSpec) string {
	if spec == nil {
		return ""
	}

	if target := targetFromReplicaRequirements(spec.ReplicaRequirements); target != "" {
		return target
	}

	for _, comp := range spec.Components {
		if target := targetFromComponentRequirements(comp.ReplicaRequirements); target != "" {
			return target
		}
	}

	return ""
}

func targetFromReplicaRequirements(req *workv1alpha2.ReplicaRequirements) string {
	if req == nil {
		return ""
	}
	return targetFromNodeClaim(req.NodeClaim)
}

func targetFromComponentRequirements(req *workv1alpha2.ComponentReplicaRequirements) string {
	if req == nil {
		return ""
	}
	return targetFromNodeClaim(req.NodeClaim)
}

func targetFromNodeClaim(claim *workv1alpha2.NodeClaim) string {
	if claim == nil || claim.NodeSelector == nil {
		return ""
	}
	if target, ok := claim.NodeSelector[AffinityLabelKey]; ok {
		return target
	}
	return ""
}
