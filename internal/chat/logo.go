package chat

import "alocal/internal/brand"

// logoLines is kept as a small compatibility helper for tests and for the
// fastfetch-style startup view. The real renderer lives in internal/brand so
// the same wordmark can be reused by other commands.
const logoLines = brand.HexagonA

func renderLogo(width int) string { return brand.Logo(true, width) }

func logoPlain() string { return brand.Logo(false, 0) }
