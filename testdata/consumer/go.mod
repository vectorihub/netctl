module example.com/consumer

go 1.25

require github.com/vectorihub/netctl v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// The consumer builds against the working tree, so the test proves the code in this
// commit works as a dependency and not only in-tree.
replace github.com/vectorihub/netctl => ../..
