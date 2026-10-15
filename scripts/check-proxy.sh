#!/bin/sh
# Read-only first-proxy checks. Never load .env or print URLs/response headers.
set -eu

usage() {
    cat <<'HELP'
用法：sh check-proxy.sh --upstream URL [--frontend URL]
      sh check-proxy.sh --frontend URL

在运行规则的 Agent 主机上检查后端直连与入口的 HTTP/TLS 连通性。
仅发送 HEAD，不跟随跳转，不读取响应正文、不修改配置、不读取 token。
忽略 curl 配置文件和环境代理；不模拟 NRE 的 Relay、出口代理或自定义请求头。
URL 仅支持 HTTP/HTTPS；请勿传入用户名、密码、查询参数或片段。
退出码：0 = 所有目标返回 2xx/3xx；1 = 需进一步检查；2 = 参数或依赖错误。
HELP
}

fail() { printf '%s\n' "$1" >&2; exit 2; }
validate_url() {
    case "$1" in
        http://*|https://*) ;;
        *) fail '地址必须使用 http:// 或 https://' ;;
    esac
    case "$1" in
        *'@'*|*'?'*|*'#'*|*[[:space:]]*) fail '地址不能包含凭据、查询参数、片段或空白；请使用服务首页地址。' ;;
    esac
    authority=${1#*://}
    authority=${authority%%/*}
    [ -n "$authority" ] || fail '地址缺少主机名。'
}

upstream=''
frontend=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        --upstream|--frontend)
            option=$1
            [ "$#" -ge 2 ] || fail '选项缺少 URL。'
            validate_url "$2"
            case "$option" in
                --upstream) upstream=$2 ;;
                --frontend) frontend=$2 ;;
            esac
            shift 2
            ;;
        -h|--help) usage; exit 0 ;;
        *) fail '未知选项；使用 --help 查看用法。' ;;
    esac
done
[ -n "$upstream$frontend" ] || fail '请提供 --upstream 或 --frontend；使用 --help 查看用法。'
command -v curl >/dev/null 2>&1 || fail '缺少 curl，请先用系统包管理器安装。'

result=0
check_url() {
    label=$1
    url=$2
    rc=0
    # -q must be first: a user's .curlrc must not inject headers, credentials or -k.
    status=$(curl -q --silent --head --output /dev/null --write-out '%{http_code}' \
        --noproxy '*' --proto '=http,https' --connect-timeout 5 --max-time 10 \
        --url "$url" 2>/dev/null) || rc=$?
    if [ "$rc" -ne 0 ]; then
        result=1
        case "$rc" in
            6) hint='DNS 解析失败：检查域名拼写及当前主机的 DNS。' ;;
            7) hint='连接失败：检查服务监听、端口、安全组和防火墙。' ;;
            28) hint='请求超时：检查网络路径、端口放行及服务响应。' ;;
            35) hint='TLS 握手失败：检查 HTTPS 协议、端口和服务 TLS 配置。' ;;
            60) hint='证书校验失败：检查证书域名、有效期和证书链，不要跳过校验。' ;;
            *) hint='请求失败：核对地址及本机 curl/网络环境。' ;;
        esac
        printf '[需检查] %s：%s（curl %s）\n' "$label" "$hint" "$rc"
        return
    fi
    case "$status" in
        2[0-9][0-9]) printf '[通过] %s：HTTP %s。\n' "$label" "$status" ;;
        3[0-9][0-9]) printf '[通过] %s：HTTP %s，收到跳转；尚未验证跳转目标。\n' "$label" "$status" ;;
        *)
            result=1
            case "$status" in
                401|403) hint='已收到 HTTP 响应，但访问受限；检查登录、来源限制或自定义请求头。' ;;
                405|501) hint='服务可能不支持 HEAD；请用浏览器验证，不能据此认定反代故障。' ;;
                404) hint='已收到 HTTP 响应；检查访问路径、入口域名和规则匹配。' ;;
                502|503|504) hint='服务或网关不可用；对照后端检查结果，并检查规则同步和节点状态。' ;;
                *) hint='收到非成功响应，请检查服务状态。' ;;
            esac
            printf '[需检查] %s：HTTP %s。%s\n' "$label" "$status" "$hint"
            ;;
    esac
}

printf '%s\n' 'NRE 连通性检查（当前主机直连，最多每个目标 10 秒）'
[ -z "$upstream" ] || check_url '后端' "$upstream"
[ -z "$frontend" ] || check_url '入口' "$frontend"
printf '%s\n' '此结果只验证当前主机的 HEAD 响应，不代表公网可达、规则匹配正确或播放正常。' \
    '下一步：在实际客户端打开入口，验证登录、播放和拖动进度；分享前仍请检查输出。'
exit "$result"
