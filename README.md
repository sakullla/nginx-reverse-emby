# nginx emby 反代指南
- 这是关于Emby 公费服/机场服的反代配置
- 目前支持单个域名的反代。 以及307重定向的反代。对于301 302的重定向，目前模板里默认重定向后的地址是https，如果是http需要自己调整模板。
- 代理后的emby支持 http1.1\http2\http3 ipv4/ipv6访问
- 支持代理多个emby，每次只要根据模板调整和申请证书即可

一键部署脚本 将 you.example.com 替换成你的域名 backend.com 替换成你要反代的emby 
```shell
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y yourdomain.com -r backend.com
```


**首次使用请查看[full.md](full.md)**
# 快速使用

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

- 使用 standalone 模式为你的域名 you.example.com 申请 ECC 证书，并放到指定位置

```shell
mkdir -p /etc/nginx/certs/you.example.com
acme.sh --issue -d you.example.com  --standalone --keylength ec-256
acme.sh --install-cert -d you.example.com --ecc --fullchain-file /etc/nginx/certs/you.example.com/cert --key-file /etc/nginx/certs/you.example.com/key --reloadcmd "nginx -s reload"
```





