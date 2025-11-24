# GlobalAdvisor 插件使用说明

## 插件功能

GlobalAdvisor 是一个评分插件（ScorePlugin），通过调用 SpringBoot 项目获取集群健康评分，为每个集群进行打分。

## 配置方式

### 方法 1：只启用 GlobalAdvisor 评分插件（推荐用于测试）

在 karmada-scheduler 的 Deployment 中添加 `--plugins` 参数：

```yaml
command:
  - /bin/karmada-scheduler
  - --kubeconfig=/etc/karmada/config/karmada.config
  - --plugins=APIEnablement,ClusterAffinity,ClusterEviction,SpreadConstraint,TaintToleration,GlobalAdvisor
  # ... 其他参数
```

**说明**：
- 保留所有过滤插件（FilterPlugin）：APIEnablement, ClusterAffinity, ClusterEviction, SpreadConstraint, TaintToleration
- 只启用 GlobalAdvisor 作为评分插件
- 禁用 ClusterAffinity 和 ClusterLocality 的评分功能

### 方法 2：从所有插件中排除其他评分插件

```yaml
command:
  - /bin/karmada-scheduler
  - --kubeconfig=/etc/karmada/config/karmada.config
  - --plugins=*,-ClusterAffinity,-ClusterLocality
  # ... 其他参数
```

**说明**：
- `*` 表示启用所有插件
- `-ClusterAffinity,-ClusterLocality` 表示禁用这两个插件（注意：ClusterAffinity 仍会作为过滤插件工作）

### 方法 3：只启用 GlobalAdvisor（不推荐，可能缺少必要的过滤逻辑）

```yaml
command:
  - /bin/karmada-scheduler
  - --kubeconfig=/etc/karmada/config/karmada.config
  - --plugins=GlobalAdvisor
  # ... 其他参数
```

⚠️ **警告**：这种方式可能导致调度器缺少必要的过滤逻辑，建议使用方法 1。

## 修改部署

### 通过 kubectl 修改

```bash
# 编辑 karmada-scheduler deployment
kubectl -n karmada-system edit deployment karmada-scheduler

# 在 command 部分添加 --plugins 参数
```

### 直接修改 yaml 文件

编辑 `artifacts/deploy/karmada-scheduler.yaml`，在 command 部分添加：

```yaml
command:
  - /bin/karmada-scheduler
  - --kubeconfig=/etc/karmada/config/karmada.config
  - --plugins=APIEnablement,ClusterAffinity,ClusterEviction,SpreadConstraint,TaintToleration,GlobalAdvisor
  - --metrics-bind-address=$(POD_IP):8080
  # ... 其他现有参数
```

然后重新部署：

```bash
kubectl apply -f artifacts/deploy/karmada-scheduler.yaml
```

## 验证插件是否生效

1. **查看调度器日志**：

```bash
kubectl -n karmada-system logs -l app=karmada-scheduler --tail=100 | grep GlobalAdvisor
```

你应该能看到类似这样的日志：
```
[GlobalAdvisor] Score called for cluster=xxx
[GlobalAdvisor] got score cluster=xxx score=75.50 reason=xxx
```

2. **检查调度器启动日志**：

```bash
kubectl -n karmada-system logs -l app=karmada-scheduler --tail=50 | grep "Enable Scheduler plugin"
```

你应该能看到：
```
Enable Scheduler plugin "GlobalAdvisor"
```

## 插件配置

默认配置：
- GS URL: `http://127.0.0.1:8088`
- 超时时间: `300ms`
- 重试次数: `1`
- 缓存 TTL: `3s`
- 默认分数: `50.0`

这些配置目前是硬编码的，如果需要修改，可以编辑 `global_advisor.go` 中的常量定义。

## 注意事项

1. 确保你的 SpringBoot 服务运行在 `http://127.0.0.1:8088` 或修改代码中的默认 URL
2. SpringBoot API 接口应该是：`GET /api/advisor/score?cluster=<clusterName>`
3. 返回的 JSON 格式应该是：
   ```json
   {
     "clusterName": "cluster1",
     "healthScore": 75.5,
     "reason": "healthy"
   }
   ```
4. 分数范围会被限制在 0-100 之间




## 本地仅启用 GlobalAdvisor（速查）

- 目标：本机 `go run` 直连集群，只启用自定义评分插件进行联调。
  - 启动命令：仅启用必要 Filter（APIEnablement、TaintToleration）和 GlobalAdvisor，关闭估算器与选举。

```powershell
go run cmd/scheduler/main.go `
  --kubeconfig="E:\karmada-config" `
  --plugins=APIEnablement,TaintToleration,GlobalAdvisor `
  --enable-scheduler-estimator=false `
  --leader-elect=false `
  --metrics-bind-address=0.0.0.0:8080 `
  --health-probe-bind-address=0.0.0.0:10351 `
  --logging-format=text `
  --v=4
```

- 可选：如需保留选举避免与集群实例冲突，使用独立租约名：

```powershell
--leader-elect=true --leader-elect-resource-name=karmada-scheduler-dev
```

### 为什么需要 spreadConstraints 来“按分数选集群”
- 未配置 `spreadConstraints` 时，选择阶段会“Select all clusters”，副本分配优先“可分配副本/聚合/保留历史”，不一定严格按最高分。
- 一旦配置 `spreadConstraints`，选择列表按分数组织，GlobalAdvisor 的分数会直接影响被选集群集合，效果更贴近预期。

推荐最小策略（便于观察分数主导）：

```yaml
spec:
  placement:
    spreadConstraints:
      - spreadByField: Cluster
        minGroups: 1
    replicaScheduling:
      replicaSchedulingType: Divided
      replicaDivisionPreference: Aggregated
  replicas: 1
```

触发“Fresh 重算”（避免保留上次分配的粘性，可选）：

```bash
kubectl -n <ns> patch resourcebinding <name> --type merge -p \
'{"spec":{"rescheduleTriggeredAt":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"}}'
```

验证：
- 启动日志包含：Enable Scheduler plugin "GlobalAdvisor"
- 调度日志包含：GlobalAdvisor Score called / got score
- 按上面策略与副本=1，通常会选分数最高的集群

go run cmd/scheduler/main.go `
  --kubeconfig="E:\karmada-config" `
  --plugins=APIEnablement,TaintToleration,GlobalAdvisor `
  --enable-scheduler-estimator=false `
  --leader-elect=false `
  --metrics-bind-address=0.0.0.0:8080 `
  --health-probe-bind-address=0.0.0.0:10351 `
  --logging-format=text `
  --v=4
