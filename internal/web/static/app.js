(() => {
  const state = { node: null, service: null };
  const $ = (selector) => document.querySelector(selector);
  const elements = {
    account: $("#account"), username: $("#username"), loginPanel: $("#login-panel"), loginForm: $("#login-form"), loginError: $("#login-error"),
    workspace: $("#workspace"), notice: $("#notice"), nodes: $("#nodes"), services: $("#services"), files: $("#files"),
    nodeCount: $("#node-count"), fileCount: $("#file-count"), title: $("#workspace-title")
  };
  async function request(path, options = {}) {
    const response = await fetch(path, { credentials: "same-origin", ...options, headers: { "Content-Type": "application/json", ...(options.headers || {}) } });
    if (response.status === 401) { showLogin(); throw new Error("登录状态已失效，请重新登录。"); }
    if (!response.ok) { const payload = await response.json().catch(() => ({})); throw new Error(payload.message || "请求失败，请稍后重试。"); }
    return response.status === 204 ? null : response.json();
  }
  function setNotice(message = "") { elements.notice.textContent = message; }
  function showLogin() { elements.account.hidden = true; elements.workspace.hidden = true; elements.loginPanel.hidden = false; }
  function showWorkspace(username) { elements.username.textContent = username; elements.account.hidden = false; elements.loginPanel.hidden = true; elements.workspace.hidden = false; }
  function empty(target, message) { target.replaceChildren(); const p = document.createElement("p"); p.className = "empty"; p.textContent = message; target.append(p); }
  function formatSize(bytes) { if (!Number.isFinite(bytes)) return ""; const units = ["B", "KiB", "MiB", "GiB"]; let value = bytes; let index = 0; while (value >= 1024 && index < units.length - 1) { value /= 1024; index++; } return `${value.toFixed(index ? 1 : 0)} ${units[index]}`; }
  function statusClass(status) { return status === "Ready" ? "ready" : status === "AgentUnavailable" ? "offline" : "warning"; }
  async function loadNodes() {
    setNotice("正在获取节点..."); state.node = null; state.service = null; empty(elements.services, "先选择节点。"); empty(elements.files, "先选择服务。"); elements.fileCount.textContent = "";
    try {
      const data = await request("/api/v1/nodes"); elements.nodes.replaceChildren(); elements.nodeCount.textContent = `${data.items.length} 个`;
      for (const node of data.items) {
        const item = $("#node-template").content.firstElementChild.cloneNode(true); item.querySelector(".row-title").textContent = node.name;
        const badge = item.querySelector(".badge"); badge.textContent = node.status; badge.classList.add(statusClass(node.status)); item.querySelector(".row-detail").textContent = node.reason || "节点与代理均正常";
        item.disabled = !node.agentReady; item.addEventListener("click", () => selectNode(node, item)); elements.nodes.append(item);
      }
      if (!data.items.length) empty(elements.nodes, "没有可见节点。"); setNotice("");
    } catch (error) { empty(elements.nodes, "节点加载失败。"); setNotice(error.message); }
  }
  async function selectNode(node, button) {
    state.node = node; state.service = null; elements.nodes.querySelectorAll(".active").forEach((x) => x.classList.remove("active")); button.classList.add("active"); elements.title.textContent = node.name; empty(elements.files, "先选择服务。"); elements.fileCount.textContent = ""; setNotice("正在获取服务...");
    try {
      const data = await request(`/api/v1/nodes/${encodeURIComponent(node.name)}/services`); elements.services.replaceChildren();
      for (const service of data.services) { const item = $("#service-template").content.firstElementChild.cloneNode(true); item.querySelector(".row-title").textContent = service.displayName; item.querySelector(".row-detail").textContent = service.id; item.addEventListener("click", () => selectService(service, item)); elements.services.append(item); }
      setNotice("");
    } catch (error) { empty(elements.services, "服务加载失败。"); setNotice(error.message); }
  }
  async function selectService(service, button) {
    state.service = service; elements.services.querySelectorAll(".active").forEach((x) => x.classList.remove("active")); button.classList.add("active"); setNotice("正在读取文件列表...");
    try {
      const base = `/api/v1/nodes/${encodeURIComponent(state.node.name)}/services/${encodeURIComponent(service.id)}/files`; const data = await request(base); elements.files.replaceChildren(); elements.fileCount.textContent = `${data.items.length} 个`;
      for (const file of data.items) { const item = $("#file-template").content.firstElementChild.cloneNode(true); item.querySelector(".row-title").textContent = file.name; item.querySelector(".row-detail").textContent = `${formatSize(file.size)}${file.modTime ? ` · ${file.modTime}` : ""}`; const link = item.querySelector(".download"); link.href = `${base}/${encodeURIComponent(file.name)}/download`; link.textContent = "下载"; elements.files.append(item); }
      if (!data.items.length) empty(elements.files, "当前服务没有可下载日志。"); setNotice("");
    } catch (error) { empty(elements.files, "文件列表加载失败。"); elements.fileCount.textContent = ""; setNotice(error.message); }
  }
  elements.loginForm.addEventListener("submit", async (event) => { event.preventDefault(); elements.loginError.textContent = ""; const form = new FormData(elements.loginForm); try { const session = await request("/api/v1/login", { method: "POST", body: JSON.stringify({ username: form.get("username"), password: form.get("password") }) }); showWorkspace(session.username); await loadNodes(); } catch (error) { elements.loginError.textContent = error.message; } });
  $("#logout").addEventListener("click", async () => { try { await request("/api/v1/logout", { method: "POST", body: "{}" }); } finally { state.node = null; state.service = null; showLogin(); } });
  $("#refresh").addEventListener("click", loadNodes);
  request("/api/v1/session").then((session) => { showWorkspace(session.username); return loadNodes(); }).catch(() => showLogin());
})();
