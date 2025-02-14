# nginx emby 反代指南
- 这是关于Emby 公费服/机场服的反代配置
- 目前支持单个域名的反代。 以及307重定向的反代。对于301 302的重定向，目前模板里默认重定向后的地址是https，如果是http需要自己调整模板。
- 代理后的emby支持 http1.1\http2\http3 ipv4/ipv6访问
- 支持代理多个emby，每次只要根据模板调整和申请证书即可
- 暂时没有计划根据一键脚本进行配置

**首次使用请查看[full.md](full.md)**

# 快速使用

- 将 [p.example.com.conf](conf.d/p.example.com.conf) 拷贝成你的域名配置 比如 y.example.com.conf
```shell
cp p.example.com.conf y.example.com.conf
```

- 将y.example.com.conf里面的 p.example.com 替换为 拷贝成你的域名配置 比如 y.example.com
```shell
sed -i 's/p.example.com/y.example.com/g' y.example.com.conf
```

- 将y.example.com.conf里面的 emby.example.com 替换为要反代的域名 r.example.com
```shell
sed -i 's/emby.example.com/r.example.com/g' y.example.com.conf
```

- 将 y.example.com.conf 放到 /etc/nginx/conf.d 下面
```shell
mv y.example.com.conf /etc/nginx/conf.d/
```

- 使用 standalone 模式为你的域名 p.example.com 申请 ECC 证书，并放到指定位置

```shell
mkdir -p /etc/nginx/certs/p.example.com
acme.sh --issue -d p.example.com  --standalone --keylength ec-256
acme.sh --install-cert -d p.example.com --ecc --fullchain-file /etc/nginx/certs/p.example.com/cert --key-file /etc/nginx/certs/p.example.com/key --reloadcmd "nginx -s reload"
```





