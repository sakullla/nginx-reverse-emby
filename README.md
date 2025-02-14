# nginx-reverse-emby
## nginx emby
- 这是关于Emby 公费服/机场服的反代配置
- 目前支持单个域名的反代。 以及307重定向的反代。对于301 302的重定向暂时没有验证。
- 代理后的emby支持 http1.1\http2\http3 ipv4/ipv6访问
- 支持代理多个emby，每次只要根据模板调整和申请证书即可
- 暂时没有计划根据一键脚本进行配置


1. 安装[Nginx](http://nginx.org/en/linux_packages.html)

- Debian 10/11/12

```
apt install -y gnupg2 ca-certificates lsb-release debian-archive-keyring && curl https://nginx.org/keys/nginx_signing.key | gpg --dearmor > /usr/share/keyrings/nginx-archive-keyring.gpg && echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/debian `lsb_release -cs` nginx" > /etc/apt/sources.list.d/nginx.list && echo -e "Package: *\nPin: origin nginx.org\nPin: release o=nginx\nPin-Priority: 900\n" > /etc/apt/preferences.d/99nginx && apt update -y && apt install -y nginx && mkdir -p /etc/systemd/system/nginx.service.d && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf && systemctl daemon-reload
```

- Ubuntu 18.04/20.04/22.04

```
apt install -y gnupg2 ca-certificates lsb-release ubuntu-keyring && curl https://nginx.org/keys/nginx_signing.key | gpg --dearmor > /usr/share/keyrings/nginx-archive-keyring.gpg && echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/ubuntu `lsb_release -cs` nginx" > /etc/apt/sources.list.d/nginx.list && echo -e "Package: *\nPin: origin nginx.org\nPin: release o=nginx\nPin-Priority: 900\n" > /etc/apt/preferences.d/99nginx && apt update -y && apt install -y nginx && mkdir -p /etc/systemd/system/nginx.service.d && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf && systemctl daemon-reload
```

- 卸载Nginx

```
systemctl stop nginx && apt purge -y nginx && rm -r /etc/systemd/system/nginx.service.d/
```

2. 修改配置文件
- 将项目里的[nginx.conf](nginx.conf) 复制到 /etc/nginx/
```shell
cp nginx.conf /etc/nginx/
```
- 根据 [p.example.com.conf](conf.d/p.example.com.conf) 修改成你的域名跟要代理的emby,并将文件放到 /etc/nginx/conf.d 下面
- p.example.com  修改为你的域名
- emby.example.com 修改为要反代的域名

3. 使用[acme](https://github.com/acmesh-official/acme.sh)申请SSL证书

- [点击查看详细步骤](acme.md)
- SSL证书有效期是90天，acme每60天自动更新一次





