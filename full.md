## 1. 安装[Nginx](http://nginx.org/en/linux_packages.html)

- Debian 10/11/12

```shell
apt install -y gnupg2 ca-certificates lsb-release debian-archive-keyring && curl https://nginx.org/keys/nginx_signing.key | gpg --dearmor > /usr/share/keyrings/nginx-archive-keyring.gpg && echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/debian `lsb_release -cs` nginx" > /etc/apt/sources.list.d/nginx.list && echo -e "Package: *\nPin: origin nginx.org\nPin: release o=nginx\nPin-Priority: 900\n" > /etc/apt/preferences.d/99nginx && apt update -y && apt install -y nginx && mkdir -p /etc/systemd/system/nginx.service.d && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf && systemctl daemon-reload &&rm -f /etc/nginx/conf.d/default.conf &&systemctl enable --now nginx```
```

- Ubuntu 18.04/20.04/22.04

```shell
apt install -y gnupg2 ca-certificates lsb-release ubuntu-keyring && curl https://nginx.org/keys/nginx_signing.key | gpg --dearmor > /usr/share/keyrings/nginx-archive-keyring.gpg && echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/ubuntu `lsb_release -cs` nginx" > /etc/apt/sources.list.d/nginx.list && echo -e "Package: *\nPin: origin nginx.org\nPin: release o=nginx\nPin-Priority: 900\n" > /etc/apt/preferences.d/99nginx && apt update -y && apt install -y nginx && mkdir -p /etc/systemd/system/nginx.service.d && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf && systemctl daemon-reload &&rm -f /etc/nginx/conf.d/default.conf &&systemctl enable --now nginx
```

- 卸载Nginx
```shell
systemctl stop nginx && apt purge -y nginx && rm -r /etc/systemd/system/nginx.service.d/
```

## 2.修改配置文件

- 将项目里的[nginx.conf](nginx.conf) 复制到 /etc/nginx/
```shell
cp nginx.conf /etc/nginx/
```
- 将 [p.example.com.conf](conf.d/p.example.com.conf) 拷贝成你的域名配置 比如 you.example.com.conf
```shell
cp p.example.com.conf you.example.com.conf
```

- 将you.example.com.conf里面的 p.example.com 替换为 拷贝成你的域名配置 比如 you.example.com
```shell
sed -i 's/p.example.com/you.example.com/g' you.example.com.conf
```

- 将you.example.com.conf里面的 emby.example.com 替换为要反代的域名 r.example.com
```shell
sed -i 's/emby.example.com/r.example.com/g' you.example.com.conf
```

- 将 you.example.com.conf 放到 /etc/nginx/conf.d 下面
```shell
mv you.example.com.conf /etc/nginx/conf.d/
```

## 3. 使用[acme](https://github.com/acmesh-official/acme.sh)申请SSL证书

- 安装acme

```shell
apt install -y socat
```

```shell
curl https://get.acme.sh | sh
```

```shell
source ~/.bashrc
```

- 设置acme自动更新

```shell
acme.sh --upgrade --auto-upgrade
```

- 将默认 CA 更改为 Let's Encrypt

```shell
acme.sh --set-default-ca --server letsencrypt
```

- 使用 standalone 模式为 you.example.com 申请 ECC 证书，并放到指定位置

```shell
mkdir -p /etc/nginx/certs/you.example.com
acme.sh --issue -d you.example.com  --standalone --keylength ec-256
acme.sh --install-cert -d you.example.com --ecc --fullchain-file /etc/nginx/certs/you.example.com/cert --key-file /etc/nginx/certs/you.example.com/key --reloadcmd "nginx -s reload"
``````

- 强制更新证书

```shell
acme.sh --renew -d you.example.com--force --ecc
```





