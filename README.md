# Unbundle

A tool for unbundling packages or bundled source files.

Contains some packages from `internal` of [tools](https://github.com/golang/tools.git)

Note : previously unbundled files get deleted on re-run.

Usage: unbundle [options] <package_or_file>

Where the options are :

  -dst path
     	set destination unbundled path (e.g. `D:\inspect\rebundle`) - default `unbundled`

  -newpkg name
     	set destination package name and folder inside the destination `path` (e.g. `http`)

  -privfunc string
     	private functions file (no extension) (default "private_fns")

  -pubfunc string
     	public functions file (no extension) (default "public_fns")

  -types string
     	types definition file (no extension) (default "defs")

# Examples    	

For unbundling a single file:

```
unbundle --newpkg="http" --dst="unbundled" --privfunc="_private" --pubfunc="_public" --types="_types" /usr/local/go/src/net/http/server.go
```

For unbundling a package:

```
unbundle --types="_types" --newpkg="http" --dst="unbundled" --privfunc="_prv" --pubfunc="_pub" "net/http"
```

# Why?

Splitting the atoms. 