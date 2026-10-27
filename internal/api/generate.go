// Package api is the contract between the daemon and its clients: the types,
// server interface and client generated from api/openapi.yaml, and the error
// codes.
package api

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../api/openapi.yaml
