// Package data embeds the editable reference tables that ship with Alocal.
package data

import _ "embed"

// GPUsJSON is the GPU lookup table.
//
//go:embed gpus.json
var GPUsJSON []byte

// ModelsJSON is the model catalog used by scoring and recommendations.
//
//go:embed models.json
var ModelsJSON []byte

// DiscoveryJSON is the interactive catalogue's embedded repository snapshot.
//
//go:embed discovery.json
var DiscoveryJSON []byte
