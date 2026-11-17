// Package version expose la version de gopit, injectée à la compilation :
//
//	go build -ldflags "-X github.com/laurentpoirierfr/gopit/internal/version.Version=1.0.0"
package version

// Version vaut « dev » hors d'une compilation via le Makefile.
var Version = "dev"
