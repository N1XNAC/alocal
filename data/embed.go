// Package data embeds the editable reference tables that ship with Alocal.
package data

import _ "embed"

// GPUsJSON is the GPU lookup table.
//
//go:embed gpus.json
var GPUsJSON []byte

// ModelsJSON is the model catalog.
//
//go:embed models.json
var ModelsJSON []byte
