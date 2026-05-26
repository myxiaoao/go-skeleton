# K8s 部署模板（Kustomize）

最小可用的 K8s 部署基线。**不引 Helm**——一份 yaml 比 Helm chart + values 易读、易 diff、易移植；真要 Helm 化由各团队自行包装。

## 目录

```
deploy/k8s/
├── base/                          # 与环境无关的资源基线
│   ├── kustomization.yaml         # 资源清单 + commonLabels
│   ├── namespace.yaml             # go-skeleton namespace
│   ├── configmap.yaml             # 非敏感 env（与 .env.example 对齐）
│   ├── secret.example.yaml        # 敏感 env 占位（不要直接 apply）
│   ├── api-deployment.yaml        # API Deployment + Service（含 metrics 端口）
│   ├── worker-deployment.yaml     # Worker Deployment
│   ├── migrate-job.yaml           # 一次性迁移 Job
│   ├── hpa.yaml                   # API CPU HPA
│   ├── servicemonitor.yaml        # Prometheus Operator scrape 配置
│   ├── pdb.yaml                   # PodDisruptionBudget（API minAvailable=1）
│   └── networkpolicy.yaml         # NetworkPolicy（API/worker ingress 收紧）
└── overlays/
    └── production/
        └── kustomization.yaml     # 生产 overlay（镜像 tag / 副本数 / HPA 边界）
```

## 部署前要做的事

1. **创建 Secret**（**不要**直接 apply `secret.example.yaml`，那里全是占位串）：

   ```sh
   kubectl create namespace go-skeleton

   kubectl -n go-skeleton create secret generic go-skeleton-secrets \
     --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
     --from-literal=POSTGRES="postgres://app:STRONG-PWD@pg-host:5432/app?sslmode=require" \
     --from-literal=REDIS_ADDR="redis-host:6379" \
     --from-literal=REDIS_PASSWORD="STRONG-REDIS-PWD"
   ```

   生产推荐用 sealed-secrets / external-secrets / Vault，避免明文落盘。

2. **改 overlay 的镜像名 + tag**（`overlays/production/kustomization.yaml`）：

   ```sh
   cd deploy/k8s/overlays/production
   kustomize edit set image go-skeleton-api=registry/your-org/go-skeleton-api:$VERSION
   kustomize edit set image go-skeleton-worker=registry/your-org/go-skeleton-worker:$VERSION
   kustomize edit set image go-skeleton-migrate=registry/your-org/go-skeleton-migrate:$VERSION
   ```

3. **确认集群有可选依赖**（缺哪个就注释掉对应 yaml）：

   | 资源 | 集群要装 |
   |---|---|
   | `hpa.yaml` | metrics-server（`kubectl top pods` 能跑就有） |
   | `servicemonitor.yaml` | Prometheus Operator（`kubectl get crd servicemonitors.monitoring.coreos.com`） |
   | `networkpolicy.yaml` | NetworkPolicy CNI 实现（Calico / Cilium / Weave 等）；普通 kindnet 没装时 yaml 被接受但策略不生效 |
   | Deployment `reloader.stakater.com/auto` annotation | [stakater/Reloader](https://github.com/stakater/Reloader)（缺则改 ConfigMap 后要手动 `kubectl rollout restart`） |

## NetworkPolicy / PDB 适配

- `pdb.yaml` 默认 `minAvailable=1`：保单副本时阻止 voluntary 驱逐，多副本
  时允许逐个滚动。生产推荐 `minAvailable: 2` 或 `maxUnavailable: 25%`。
- `networkpolicy.yaml` 给 API metrics 端口 9090 限定 `namespaceSelector:
  matchLabels: { purpose: monitoring }`——Prometheus 所在 namespace 必须
  打 `purpose=monitoring` 标签才能 scrape。没装监控时 metrics 端口被锁死
  是预期（fail-secure）。
- 不需要 NetworkPolicy（开发集群 / mesh 接管）时去 overlay 加
  `patches: [{path: ..., target: {kind: NetworkPolicy}}]` 删掉即可，不必
  动 base。

## 部署顺序

```sh
cd deploy/k8s/overlays/production

# 1. 先 apply 非业务资源（namespace / configmap / secret 引用）。
#    kustomize build 后过滤出 Job / Deployment 之外的所有资源：
kustomize build . | grep -vE "^(kind: Job|kind: Deployment|kind: HorizontalPodAutoscaler)" | kubectl apply -f -
# 实操中更常见的是一次 apply 全部，K8s 自己等依赖就绪——但这条命令清晰展示
# "先建底座再跑迁移最后滚业务"的语义。

# 2. 跑迁移 Job 等成功。失败的话 kubectl logs job/go-skeleton-migrate 看原因。
kubectl -n go-skeleton delete job go-skeleton-migrate --ignore-not-found
kustomize build . | yq 'select(.kind == "Job")' | kubectl apply -f -
kubectl -n go-skeleton wait --for=condition=complete --timeout=600s job/go-skeleton-migrate

# 3. 最后 apply Deployment / HPA / ServiceMonitor。
kustomize build . | kubectl apply -f -
```

> 简化版（适合首次部署 / 测试集群）：直接 `kustomize build . | kubectl apply -f -`，K8s 会按依赖关系自己调度。Migrate Job 启动比 API Pod 慢的话 API 第一次会 503（看不到表），但 readiness probe 会兜住。

## 回滚

| 触发 | 命令 |
|---|---|
| 业务镜像回滚 | `kubectl -n go-skeleton rollout undo deployment/go-skeleton-api`（worker 同理） |
| Schema 破坏性变更回滚 | 先做 expand-contract 验证；应急走 `pg_dump` 备份恢复（详见 `docs/deploy.md` §5） |
| ConfigMap 误改 | 改回 YAML 重新 apply，Reloader 触发 rollout；无 Reloader 时手 `rollout restart` |

## 与 systemd 路径的对应

| systemd 单元 | K8s 资源 |
|---|---|
| `go-skeleton-api.service` | `api-deployment.yaml` Deployment + Service |
| `go-skeleton-worker.service` | `worker-deployment.yaml` Deployment |
| `go-skeleton-migrate.service` | `migrate-job.yaml` Job |
| `WatchdogSec=30s` | livenessProbe + readinessProbe（K8s 概念不同但意图相同） |
| `LimitNOFILE=65535` | 由节点 ulimit / containerd 默认值兜底；高并发场景需在 Pod spec 加 `ulimits` |
| `EnvironmentFile=/etc/go-skeleton/.env` | `envFrom: configMapRef + secretRef` |

systemd 路径详见 `docs/deploy.md` §3–§9；那边的运维原则（迁移向后兼容、expand-contract、advisory lock 并发安全等）在 K8s 路径**完全适用**——只是命令载体换成 kubectl。
