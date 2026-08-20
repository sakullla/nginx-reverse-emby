module github.com/sakullla/nginx-reverse-emby/go-agent

go 1.27.0

require (
	github.com/quic-go/quic-go v0.61.0
	github.com/sakullla/nginx-reverse-emby/plugin-sdk v0.0.0
	github.com/tetratelabs/wazero v1.12.0
	golang.org/x/crypto v0.54.0
	golang.org/x/net v0.57.0
	golang.org/x/sys v0.47.0
	google.golang.org/grpc v1.66.2
	google.golang.org/protobuf v1.34.2
)

replace github.com/sakullla/nginx-reverse-emby/plugin-sdk => ../plugin-sdk

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	go.uber.org/mock v0.6.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240604185151-ef581f913117 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
