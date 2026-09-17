# Kubernetes 日志门户设计评审修订实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将已确认的设计评审问题同步修订到 Markdown 设计稿和 Word 开发文档生成器，并生成保留原件的 v1.1-r2 Word 文档。

**Architecture:** 保持“Go 中央门户 + Nginx DaemonSet”不变，仅补齐实现契约和验收条件。Markdown 负责产品与架构边界，Word 开发文档负责可直接编码和部署的细化规则。

**Tech Stack:** Markdown、Python、python-docx、PowerShell、LibreOffice 渲染器（若环境可用）。

---

### Task 1: 修订设计稿

**Files:**
- Modify: `docs/superpowers/specs/2026-07-09-kubernetes-log-download-portal-design.md`

- [x] 明确发现缓存新鲜度按成功 list/relist 或 watch BOOKMARK/事件推进 resourceVersion 计算，而不是对象最后变更时间或仅建立 watch 连接。
- [x] 明确文件列表使用响应体字节上限和流式 JSON 解码，在第 10,001 项终止请求。
- [x] 明确非 root Agent 的宿主机目录读取权限前置条件，并定义 probeFileName/probe_directories.txt readiness 机制。
- [x] 明确 Ingress 禁止响应缓冲和磁盘落盘，并完成外部入口大文件验收。
- [x] 补充 Kubernetes DNS subdomain、502 上游错误、Range 压缩约束、正文空转超时、requestId 校验、路径确定性规则、CSP、审计留存与 NetworkPolicy 标签冒充边界。

### Task 2: 修订 Word 开发文档生成器

**Files:**
- Modify: `docs/build_dev_document.py`
- Create: `docs/Kubernetes节点日志下载门户开发文档-v1.1.docx`

- [x] 将版本更新为 v1.1-r2，并让生成器输出 v1.1 文件以保留 v1.0 原件。
- [x] 将 Task 1 的全部规则同步到对应配置、API、发现、Agent、下载、部署、测试和验收章节。
- [x] 修正节点响应模型，明确 `nodeReady`、`agentReady`、派生 `status` 与下载阻断规则。

### Task 3: 验证

**Files:**
- Verify: `docs/superpowers/specs/2026-07-09-kubernetes-log-download-portal-design.md`
- Verify: `docs/Kubernetes节点日志下载门户开发文档-v1.1.docx`

- [x] 扫描两份文档，确认旧的矛盾表述已消失且新规则全部存在。
- [x] 使用 python-docx 重新读取 v1.1，确认段落、表格、版本和关键修订内容。
- [x] 运行 DOCX 渲染器；LibreOffice/soffice 缺失，已记录该环境限制并完成结构、表格、标题、可访问性和内容检查。
