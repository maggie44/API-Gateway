// Package openapiv1 contains generation directives for the gateway OpenAPI contract.
package openapiv1

//go:generate go tool oapi-codegen -config ./cfg.yaml -generate types -o ../../internal/clients/http/apigateway/generated/models.gen.go ./api.yaml
//go:generate go tool oapi-codegen -config ./cfg.yaml -generate client -o ../../internal/clients/http/apigateway/generated/client.gen.go ./api.yaml
//go:generate go tool oapi-codegen -config ./cfg.yaml -generate types -o ../../internal/infrastructure/http/generated/models.gen.go ./api.yaml
//go:generate go tool oapi-codegen -config ./cfg.yaml -generate chi-server -o ../../internal/infrastructure/http/generated/apigateway.gen.go ./api.yaml
