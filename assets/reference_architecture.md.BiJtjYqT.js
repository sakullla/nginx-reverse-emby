import{_ as a,o as n,c as e,a2 as p}from"./chunks/framework.CuEaBji2.js";const u=JSON.parse('{"title":"架构与设计","description":"","frontmatter":{},"headers":[],"relativePath":"reference/architecture.md","filePath":"reference/architecture.md","lastUpdated":1782021666000}'),l={name:"reference/architecture.md"};function r(i,s,t,c,o,b){return n(),e("div",null,[...s[0]||(s[0]=[p(`<h1 id="架构与设计" tabindex="-1">架构与设计 <a class="header-anchor" href="#架构与设计" aria-label="Permalink to &quot;架构与设计&quot;">​</a></h1><p>Nginx-Reverse-Emby 是一个纯 Go 实现的反向代理控制面，为 Emby、Jellyfin 以及任意 HTTP/TCP 服务设计。默认通过 Docker Compose 部署，内置一个 <code>local</code> Agent。</p><h2 id="组件关系" tabindex="-1">组件关系 <a class="header-anchor" href="#组件关系" aria-label="Permalink to &quot;组件关系&quot;">​</a></h2><div class="language-text vp-adaptive-theme line-numbers-mode"><button title="Copy Code" class="copy"></button><span class="lang">text</span><pre class="shiki shiki-themes github-light github-dark vp-code" tabindex="0"><code><span class="line"><span>控制面</span></span>
<span class="line"><span>├─ Vue 3 面板</span></span>
<span class="line"><span>├─ Go API 服务</span></span>
<span class="line"><span>│  ├─ /api/* 和 /panel-api/* 路由</span></span>
<span class="line"><span>│  ├─ Agent 注册与管理</span></span>
<span class="line"><span>│  ├─ 规则、证书、Relay、WireGuard、出口配置存储</span></span>
<span class="line"><span>│  ├─ 流量统计与额度</span></span>
<span class="line"><span>│  └─ 版本策略分发</span></span>
<span class="line"><span>├─ local Agent（内置）</span></span>
<span class="line"><span>│  ├─ HTTP 代理引擎</span></span>
<span class="line"><span>│  ├─ TCP/UDP 代理</span></span>
<span class="line"><span>│  ├─ Relay 隧道</span></span>
<span class="line"><span>│  └─ WireGuard / 流量采集</span></span>
<span class="line"><span>└─ SQLite / PostgreSQL / MySQL</span></span></code></pre><div class="line-numbers-wrapper" aria-hidden="true"><span class="line-number">1</span><br><span class="line-number">2</span><br><span class="line-number">3</span><br><span class="line-number">4</span><br><span class="line-number">5</span><br><span class="line-number">6</span><br><span class="line-number">7</span><br><span class="line-number">8</span><br><span class="line-number">9</span><br><span class="line-number">10</span><br><span class="line-number">11</span><br><span class="line-number">12</span><br><span class="line-number">13</span><br><span class="line-number">14</span><br></div></div><p><strong>远程 Agent</strong> 在其他服务器上运行，通过心跳拉取模式连接控制面。Agent 只需要出站网络访问控制面，控制面从不主动连接 Agent——这让 NAT 和防火墙配置极简。</p><h2 id="面板布局" tabindex="-1">面板布局 <a class="header-anchor" href="#面板布局" aria-label="Permalink to &quot;面板布局&quot;">​</a></h2><div class="language-text vp-adaptive-theme line-numbers-mode"><button title="Copy Code" class="copy"></button><span class="lang">text</span><pre class="shiki shiki-themes github-light github-dark vp-code" tabindex="0"><code><span class="line"><span>仪表盘</span></span>
<span class="line"><span>  ├─ 节点状态</span></span>
<span class="line"><span>  ├─ 流量概览</span></span>
<span class="line"><span>  └─ 热门规则和节点</span></span>
<span class="line"><span></span></span>
<span class="line"><span>流量管理</span></span>
<span class="line"><span>  ├─ HTTP 规则</span></span>
<span class="line"><span>  └─ L4 规则</span></span>
<span class="line"><span></span></span>
<span class="line"><span>基础设施</span></span>
<span class="line"><span>  ├─ 证书</span></span>
<span class="line"><span>  ├─ Relay 监听器</span></span>
<span class="line"><span>  ├─ WireGuard 配置</span></span>
<span class="line"><span>  └─ 节点管理</span></span>
<span class="line"><span></span></span>
<span class="line"><span>设置</span></span>
<span class="line"><span>  ├─ 常规</span></span>
<span class="line"><span>  ├─ 出口配置</span></span>
<span class="line"><span>  ├─ 数据管理</span></span>
<span class="line"><span>  └─ 关于</span></span>
<span class="line"><span></span></span>
<span class="line"><span>版本策略（独立页面）</span></span></code></pre><div class="line-numbers-wrapper" aria-hidden="true"><span class="line-number">1</span><br><span class="line-number">2</span><br><span class="line-number">3</span><br><span class="line-number">4</span><br><span class="line-number">5</span><br><span class="line-number">6</span><br><span class="line-number">7</span><br><span class="line-number">8</span><br><span class="line-number">9</span><br><span class="line-number">10</span><br><span class="line-number">11</span><br><span class="line-number">12</span><br><span class="line-number">13</span><br><span class="line-number">14</span><br><span class="line-number">15</span><br><span class="line-number">16</span><br><span class="line-number">17</span><br><span class="line-number">18</span><br><span class="line-number">19</span><br><span class="line-number">20</span><br><span class="line-number">21</span><br><span class="line-number">22</span><br></div></div><h2 id="请求流程" tabindex="-1">请求流程 <a class="header-anchor" href="#请求流程" aria-label="Permalink to &quot;请求流程&quot;">​</a></h2><div class="language-text vp-adaptive-theme line-numbers-mode"><button title="Copy Code" class="copy"></button><span class="lang">text</span><pre class="shiki shiki-themes github-light github-dark vp-code" tabindex="0"><code><span class="line"><span>浏览器 → Go 控制面</span></span>
<span class="line"><span>  → 认证的 /api/* 路由</span></span>
<span class="line"><span>  → /panel-api/* 兼容别名</span></span>
<span class="line"><span>  → 公共 Agent 资源（join-agent.sh、Agent 二进制）</span></span>
<span class="line"><span>  → 构建好的前端静态文件 / SPA 回退</span></span></code></pre><div class="line-numbers-wrapper" aria-hidden="true"><span class="line-number">1</span><br><span class="line-number">2</span><br><span class="line-number">3</span><br><span class="line-number">4</span><br><span class="line-number">5</span><br></div></div><h2 id="agent-同步流程" tabindex="-1">Agent 同步流程 <a class="header-anchor" href="#agent-同步流程" aria-label="Permalink to &quot;Agent 同步流程&quot;">​</a></h2><ol><li>控制面存储每个 Agent 的期望配置和期望版本</li><li>Agent 定期向控制面发送心跳 / 同步请求</li><li>控制面返回 HTTP 规则、L4 规则、Relay 监听器、证书和版本信息</li><li>Agent 在本地应用配置，下次心跳报告当前状态</li></ol><h2 id="数据存储" tabindex="-1">数据存储 <a class="header-anchor" href="#数据存储" aria-label="Permalink to &quot;数据存储&quot;">​</a></h2><p>默认 <strong>SQLite</strong>，数据在 <code>./data</code> 目录。不需要额外配置。</p><p>切换 PostgreSQL 或 MySQL 时设置 <code>NRE_DATABASE_DRIVER</code> 和 <code>NRE_DATABASE_DSN</code>。迁移见 <a href="./../operations/migration">数据迁移</a>。</p><table tabindex="0"><thead><tr><th>数据库</th><th>驱动值</th><th>DSN 示例</th></tr></thead><tbody><tr><td>SQLite（默认）</td><td><code>sqlite</code></td><td>自动检测</td></tr><tr><td>PostgreSQL</td><td><code>postgres</code></td><td><code>postgres://user:pass@host:5432/db?sslmode=disable</code></td></tr><tr><td>MySQL</td><td><code>mysql</code></td><td><code>user:pass@tcp(host:3306)/db?parseTime=true&amp;charset=utf8mb4</code></td></tr></tbody></table><h2 id="为什么用-host-网络" tabindex="-1">为什么用 host 网络 <a class="header-anchor" href="#为什么用-host-网络" aria-label="Permalink to &quot;为什么用 host 网络&quot;">​</a></h2><p>控制面在运行时动态创建监听端口（HTTP 规则、L4 规则、Relay 监听器）。Docker bridge 网络无法在容器启动后新增端口映射。host 网络模式让 <code>local</code> Agent 直接绑定宿主机网络接口。</p><div class="warning custom-block"><p class="custom-block-title">WARNING</p><p>规则中配置的端口直接占用宿主机端口。检查冲突并放行防火墙。</p></div>`,18)])])}const h=a(l,[["render",r]]);export{u as __pageData,h as default};
