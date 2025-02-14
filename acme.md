**你需要先购买一个域名，将主域名（或添加一个子域名），指向你VPS的IP。等待约2-5分钟，让DNS解析生效。可以通过ping你设置的域名，查看返回的IP是否正确**

**将 p.example.com 替换成你设置的域名**

**使用 standalone 模式申请/更新证书时会监听 80 端口，如果 80 端口被占用会导致失败**

- 安装acme

```
apt install -y socat
```

```
curl https://get.acme.sh | sh
```

```
source ~/.bashrc
```

- 设置acme自动更新

```
acme.sh --upgrade --auto-upgrade
```

- 将默认 CA 更改为 Let's Encrypt

```
acme.sh --set-default-ca --server letsencrypt
```

- 使用 standalone 模式为 p.example.com 申请 ECC 证书，并放到指定位置

```
mkdir -p /etc/nginx/certs/p.example.com
acme.sh --issue -d p.example.com  --standalone --keylength ec-256
acme.sh --install-cert -d p.example.com --ecc --fullchain-file /etc/nginx/certs/p.example.com/cert --key-file /etc/nginx/certs/p.example.com/key --reloadcmd "nginx -s reload"
```

- 强制更新证书

```
acme.sh --renew -d p.example.com --force --ecc
```
