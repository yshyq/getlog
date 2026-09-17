# Kubernetes 节点日志下载门户

一个由 Go 门户和每节点 Nginx Agent 组成的受控日志下载服务。浏览器端只展示配置允许的服务目录；门户通过 Kubernetes API 找到对应节点上的 Agent，再将单文件下载流式转发给已登录用户。

## 本地开发

1. 安装 Go 1.22 或更高版本。
2. 从 `configs/portal.example.yaml` 复制为 `configs/portal.local.yaml`。
3. 为 `auth.passwordHash` 配置真实 bcrypt 密码散列，并为 `auth.sessionKey` 配置随机密钥；本地 HTTP 将 `cookieSecure` 保持为 `false`。
4. 调整 `discovery.staticNodes`，使 `agentURL` 指向可用的 Nginx Agent。
5. 执行：

```powershell
go test ./...
go run ./cmd/portal -config configs/portal.local.yaml
```

打开 `http://localhost:8080` 登录。生产环境将认证信息通过环境变量 `PORTAL_USERNAME`、`PORTAL_PASSWORD_HASH`、`PORTAL_SESSION_KEY` 注入，不要写进配置文件。

## 容器与部署

构建镜像：

```powershell
docker build -t registry.example.com/log-download-portal:TAG .
```

部署清单位于 `deploy/kubernetes/log-portal.yaml`。应用前必须替换 Secret 中的占位值、门户镜像地址，以及 Nginx Agent 的宿主机日志路径和允许服务目录。该目录的权限必须允许 Agent 的非 root 用户读取，且每个允许目录都需要 `.log-portal-read-probe` 探针文件。

门户通过 Pod IP 连接 Agent，因此集群 CNI 必须支持 Pod-to-Pod 通信；Ingress 如位于门户之前，应关闭代理响应缓冲和磁盘落盘，确保大文件保持流式传输。
