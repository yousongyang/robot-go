# K8s 一键部署

部署前需要有一个可用的 Kubernetes 集群和 `kubectl` 命令行工具。

## 快速部署

```bash
# 应用所有资源（namespace → redis → configmap → pvc → deployment → service）
kubectl apply -f deploy/k8s/namespace.yaml
kubectl apply -f deploy/k8s/redis.yaml
kubectl apply -f deploy/k8s/configmap.yaml
kubectl apply -f deploy/k8s/pvc.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
```

## 访问 Web 控制台

```bash
# 端口转发到本地
kubectl -n robot-master port-forward svc/robot-master 8080:8080
# 浏览器打开 http://localhost:8080
```

## 配置说明

| 文件 | 说明 |
|------|------|
| `namespace.yaml` | 创建 `robot-master` namespace |
| `redis.yaml` | 部署 Redis 7 (单节点)，如已有 Redis 可跳过并修改 configmap |
| `configmap.yaml` | Master 的 YAML 配置，修改 `redis-addr` 指向你的 Redis |
| `pvc.yaml` | 5Gi 持久卷用于存储 HTML 报告 |
| `deployment.yaml` | Master Pod，含健康检查、资源限制 |
| `service.yaml` | ClusterIP Service，注释中有 LoadBalancer 示例 |

## 自定义镜像

如果使用自建镜像，在 `deployment.yaml` 中修改 `image` 字段：

```yaml
image: your-registry.com/robot-master:v1.0.0
```

构建 Docker 镜像示例：

```dockerfile
FROM alpine:3.19
COPY robot-master /usr/local/bin/robot-master
ENTRYPOINT ["robot-master"]
```

## 集群外 Agent 连接

如果 Agent 运行在集群外，需将 Service 改为 `LoadBalancer` 或 `NodePort`（见 `service.yaml` 注释）。
Agent 的 `-master-addr` 指向 K8s Service 的外部地址。
