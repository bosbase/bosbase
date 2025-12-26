module github.com/bosbase/bosbase-enterprise

go 1.24.4

require (
	chromem-go v0.0.0
	dbx v0.0.0
	github.com/alicebob/miniredis/v2 v2.35.0
	github.com/coocood/freecache v0.0.0
	github.com/disintegration/imaging v1.6.2
	github.com/domodwyer/mailyak/v3 v3.6.2
	github.com/dop251/goja v0.0.0-20250630131328-58d95d85e994
	github.com/dop251/goja_nodejs v0.0.0-20250409162600-f7acab6894b0
	github.com/fatih/color v1.18.0
	github.com/fsnotify/fsnotify v1.7.0
	github.com/gabriel-vasile/mimetype v1.4.10
	github.com/ganigeorgiev/fexpr v0.5.0
	github.com/go-ozzo/ozzo-validation/v4 v4.3.0
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/jackc/pgx/v5 v5.7.6
	github.com/redis/rueidis v1.0.34
	github.com/second-state/WasmEdge-go v0.14.0
	github.com/second-state/wasmedge-bindgen v0.4.1
	github.com/spf13/cast v1.9.2
	github.com/spf13/cobra v1.10.1
	golang.org/x/crypto v0.41.0
	golang.org/x/image v0.30.0
	golang.org/x/net v0.43.0
	golang.org/x/oauth2 v0.30.0
	golang.org/x/sync v0.16.0
	graphql-go v0.0.0
	langchaingo v0.0.0
	tygoja v0.0.0
	ws v0.0.0
)

require (
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
)

require (
	github.com/asaskevich/govalidator v0.0.0-20230301143203-a9d515a09cc2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/dop251/base64dec v0.0.0-20231022112746-c6c9f9a96217 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/gofrs/uuid/v5 v5.4.0
	github.com/google/pprof v0.0.0-20250317173921-a4b03ec1a45e // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/pkoukk/tiktoken-go v0.1.6 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/mod v0.27.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	golang.org/x/time v0.9.0
	golang.org/x/tools v0.36.0 // indirect
)

replace (
	chromem-go => ./chromem-go
	dbx => ./dbx
	github.com/coocood/freecache => ./freecache
	github.com/redis/rueidis => ./rueidis
	graphql-go => ./graphql-go
	langchaingo => ./langchaingo
	tygoja => ./tygoja
	ws => ./ws
)
