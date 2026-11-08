// Package hubapi is the contract between wdx-hub and its clients: the types,
// server interface and client generated from api/hub.yaml, and the error
// codes.
package hubapi

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../api/hub.yaml
