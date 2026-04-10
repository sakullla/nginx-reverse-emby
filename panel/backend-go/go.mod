module github.com/sakullla/nginx-reverse-emby/panel/backend-go

go 1.26.0

toolchain go1.26.2

require (
	github.com/glebarez/sqlite v1.11.0
	github.com/sakullla/nginx-reverse-emby/go-agent v0.0.0
	gorm.io/gorm v1.31.1
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/glebarez/go-sqlite v1.21.2 // indirect
	github.com/go-acme/lego/v4 v4.31.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/miekg/dns v1.1.69 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/mod v0.33.0 // indirect
	golang.org/x/net v0.50.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	golang.org/x/tools v0.42.0 // indirect
	modernc.org/libc v1.70.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.34.5 // indirect
)

replace github.com/sakullla/nginx-reverse-emby/go-agent => ../../go-agent
