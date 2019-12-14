package unbundle

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/badu/unbundle/imports"
	"golang.org/x/tools/go/loader"
	"io/ioutil"
	"unicode"
)

var (
	importMap = map[string]string{}
)

type (
	byter    []byte
	resulter map[string]byter
	options  struct {
		pkgName       string
		targetPkgName string
		pubUtilName   string
		privUtilName  string
		typesName     string
		isPkg         bool
	}
)

var (
	dstPath       = flag.String("dst", "", "set destination unbundled `path` (e.g. `/home/user/inspect/unbundled`) ")
	newPkgName    = flag.String("newpkg", "", "set destination package `name` and folder inside the destination `path` (e.g. `http`)")
	publicFnFile  = flag.String("pubfunc", "public_fns", "public functions file (no extension)")
	privateFnFile = flag.String("privfunc", "private_fns", "private functions file (no extension)")
	typesFile     = flag.String("types", "defs", "types definition file (no extension)")
)

// SnakeCase converts the given string to snake case following the Golang format:
// acronyms are converted to lower-case and preceded by an underscore.
func SnakeCase(s string) string {
	in := []rune(s)
	isLower := func(idx int) bool {
		return idx >= 0 && idx < len(in) && unicode.IsLower(in[idx])
	}
	isNumber := func(idx int) bool {
		return idx >= 0 && idx < len(in) && unicode.IsNumber(in[idx])
	}

	out := make([]rune, 0, len(in)+len(in)/2)
	for i, r := range in {
		if unicode.IsUpper(r) {
			r = unicode.ToLower(r)
			if i > 0 && in[i-1] != '_' && (isLower(i-1) || isLower(i+1)) {
				out = append(out, '_')
			}
		} else if i > 0 && in[i-1] != '_' && (isNumber(i - 1)) {
			out = append(out, '_')
		}
		out = append(out, r)
	}

	return string(out)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: unbundle [options] <package_or_file>\n")
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()

	log.SetPrefix("unbundle: ")
	log.SetFlags(0)

	if len(args) != 1 {
		log.Println("No arguments received.")
		usage()
		os.Exit(2)
	}

	log.Printf("Package/File : %q\nTarget Package : %q\nDestination : %q\nPublic functions file : %q\nPrivate functions file : %q\nTypes file : %q\n", args[0], *newPkgName, *dstPath, *publicFnFile, *privateFnFile, *typesFile)

	if *dstPath == "" {
		*dstPath = "unbundled"
	}
	var (
		recodes resulter
		err     error
	)

	err = os.Chdir(*dstPath)
	if err != nil {
		wkdir, _ := os.Getwd()
		log.Fatalf("error : %v looking for folder named %q in %q", err, *dstPath, wkdir)
		return
	}
	pkgName := args[0]
	opts := options{
		pkgName:       pkgName,
		targetPkgName: *newPkgName,
		pubUtilName:   *publicFnFile,
		privUtilName:  *privateFnFile,
		typesName:     *typesFile,
	}

	if strings.HasSuffix(pkgName, ".go") {
		recodes, err = unbundle(&opts)
	} else {
		opts.isPkg = true
		recodes, err = unbundle(&opts)
	}

	if err != nil {
		log.Fatal(err)
		return
	}

	if _, err = os.Stat(*newPkgName); err == nil {
		// path already exists. cleaning up
		err = os.RemoveAll(*newPkgName)
		if err != nil {
			log.Fatal(err)
			return
		}
	}

	err = os.Mkdir(*newPkgName, os.ModePerm)
	if err != nil {
		log.Fatal(err)
		return
	}

	err = os.Chdir(*newPkgName)
	if err != nil {
		log.Fatal(err)
		return
	}

	// build a map of lower case names
	lowercaseNames := make(map[string]string)
	for key, code := range recodes {
		if _, has := lowercaseNames[strings.ToLower(key)]; has {
			log.Printf("Duplicate key %q\n", key)
			// already has that key
			recodes["_"+key] = code
			delete(recodes, key)
		}
		lowercaseNames[strings.ToLower(key)] = key
	}

	for key, content := range recodes {
		//cleanup imports
		result, err := imports.Process(key, content, &imports.Options{
			TabWidth:  8,
			TabIndent: true,
			Comments:  true,
			Fragment:  true,
		})
		if err != nil {
			log.Fatalf("Error formatting : %v", err)
		}
		renamedFile := SnakeCase(key) + ".go"
		log.Printf("Writing %q into %q\n", key, renamedFile)
		err = ioutil.WriteFile(renamedFile, result, 0666)
		if err != nil {
			log.Fatalf("Error writing : %v", err)
		}
	}
}

func unbundle(opts *options) (resulter, error) {
	// Load the initial package.
	conf := loader.Config{ParserMode: parser.ParseComments, Build: &build.Default}
	conf.TypeCheckFuncBodies = func(p string) bool { return p == opts.pkgName }
	conf.AllowErrors = true
	if opts.isPkg {
		// it's a package
		conf.Import(opts.pkgName)
	} else {
		// it's a single file
		conf.FromArgs([]string{opts.pkgName}, false)
	}
	lprog, err := conf.Load()
	if err != nil {
		log.Println("Errors on load.")
		return nil, err
	}

	// Because there was a single Import call and Load succeeded,
	// InitialPackages is guaranteed to hold the sole requested package.
	info := lprog.InitialPackages()[0]

	var pkgStd = make(map[string]bool)
	var pkgExt = make(map[string]bool)

	for _, f := range info.Files {
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				log.Fatalf("invalid import path string: %v", err) // Shouldn't happen here since conf.Load succeeded.
			}
			if newPath, ok := importMap[path]; ok {
				path = newPath
			}

			var name string
			if imp.Name != nil {
				name = imp.Name.Name
			}
			spec := fmt.Sprintf("%s %q", name, path)
			if isStandardImportPath(path) {
				pkgStd[spec] = true
			} else {
				pkgExt[spec] = true
			}
		}
	}

	var utilsBuffer bytes.Buffer
	writeHeader(&utilsBuffer, opts.targetPkgName, pkgStd, pkgExt)

	var typesBuffer bytes.Buffer
	writeHeader(&typesBuffer, opts.targetPkgName, pkgStd, pkgExt)

	_, srcFileName := filepath.Split(opts.pkgName)
	srcFileName = strings.TrimSuffix(srcFileName, path.Ext(srcFileName))
	log.Printf("SrcFile : %q", srcFileName)

	result := resulter{}
	if opts.isPkg {
		result[opts.privUtilName] = utilsBuffer.Bytes()
		result[opts.pubUtilName] = utilsBuffer.Bytes()
	} else {
		result[opts.privUtilName+"_"+srcFileName] = utilsBuffer.Bytes()
		result[opts.privUtilName+"_"+srcFileName] = utilsBuffer.Bytes()
	}
	if opts.isPkg {
		result[opts.typesName] = typesBuffer.Bytes()
	} else {
		result[opts.typesName+"_"+srcFileName] = typesBuffer.Bytes()
	}
	// Modify and print each file.
	for _, f := range info.Files {
		for _, decl := range f.Decls {
			var buf bytes.Buffer
			switch fn := decl.(type) {
			case *ast.FuncDecl:
				if fn.Recv == nil {
					// it's without receiver - goes to utils.go
					buf.Reset()
					err := format.Node(&buf, lprog.Fset, &printer.CommentedNode{Node: decl, Comments: f.Comments})
					if err != nil {
						log.Fatalf("Error writing : %v", err)
					}
					buf.Write([]byte("\n\n"))
					firstLetter, _ := utf8.DecodeRuneInString(fn.Name.Name)
					if opts.isPkg {
						if unicode.IsLower(firstLetter) {
							result[opts.privUtilName] = append(result[opts.privUtilName], buf.Bytes()...)
						} else {
							result[opts.pubUtilName] = append(result[opts.pubUtilName], buf.Bytes()...)
						}
					} else {
						if unicode.IsLower(firstLetter) {
							result[opts.privUtilName+"_"+srcFileName] = append(result[opts.privUtilName+"_"+srcFileName], buf.Bytes()...)
						} else {
							result[opts.pubUtilName+"_"+srcFileName] = append(result[opts.pubUtilName+"_"+srcFileName], buf.Bytes()...)
						}
					}
					continue
				} else {
					// it's a receiver - finding in which file it should go (each receiver goes to it's own file)
					if len(fn.Recv.List) != 1 {
						log.Fatalf("Error : bad reciever.")
						continue
					}

					receiverType := fn.Recv.List[0].Type
					switch trueType := receiverType.(type) {
					case *ast.StarExpr:
						// receiver
						if id, ok := trueType.X.(*ast.Ident); ok {
							fileName := id.Name
							_, ok := result[fileName]
							buf.Reset()
							if !ok {
								log.Printf("receiver for %q was NOT found. initing...", fileName)
								writeHeader(&buf, opts.targetPkgName, pkgStd, pkgExt)
							}
							err := format.Node(&buf, lprog.Fset, &printer.CommentedNode{Node: decl, Comments: f.Comments})
							if err != nil {
								log.Fatalf("Error writing : %v", err)
							}
							buf.Write([]byte("\n\n"))
							if err != nil {
								log.Fatalf("Error writing in %q : %v", fileName, err)
							}
							if !ok {
								result[fileName] = buf.Bytes()
							} else {
								result[fileName] = append(result[fileName], buf.Bytes()...)
							}
						} else {
							log.Fatalf("Error")
						}
					case *ast.Ident:
						// interface like receiver
						fileName := trueType.Name
						_, ok := result[fileName]
						buf.Reset()
						if !ok {
							log.Printf("interface like receiver for %q was NOT found. initing...", fileName)
							writeHeader(&buf, opts.targetPkgName, pkgStd, pkgExt)
						}
						err := format.Node(&buf, lprog.Fset, &printer.CommentedNode{Node: decl, Comments: f.Comments})
						if err != nil {
							log.Fatalf("Error writing : %v", err)
						}
						buf.Write([]byte("\n\n"))
						if err != nil {
							log.Fatalf("Error writing in %q : %v", fileName, err)
						}
						if !ok {
							result[fileName] = buf.Bytes()
						} else {
							result[fileName] = append(result[fileName], buf.Bytes()...)
						}
					default:
						log.Println("Bad receiver!!!", trueType)
					}

				}
			case *ast.GenDecl:
				// it's a generic declaration (type, var, const)
				if fn.Tok == token.IMPORT {
					// imports are added per file
					continue
				}
				switch fn.Tok {
				case token.TYPE:
					for _, spec := range fn.Specs {
						value, ok := spec.(*ast.TypeSpec)
						if ok {
							fileName := value.Name.Name
							_, ok := result[fileName]
							buf.Reset()
							if !ok {
								log.Printf("Creating file for type : %q", fileName)
								writeHeader(&buf, opts.targetPkgName, pkgStd, pkgExt)
							}
							err := format.Node(&buf, lprog.Fset, &printer.CommentedNode{Node: decl, Comments: f.Comments})
							if err != nil {
								log.Fatalf("Error writing : %v", err)
							}
							buf.Write([]byte("\n\n"))
							result[fileName] = append(result[fileName], buf.Bytes()...)
						}
					}
				default:
					buf.Reset()
					err := format.Node(&buf, lprog.Fset, &printer.CommentedNode{Node: decl, Comments: f.Comments})
					if err != nil {
						log.Fatalf("Error writing : %v", err)
					}
					buf.Write([]byte("\n\n"))
					// writing into types file
					if opts.isPkg {
						result[opts.typesName] = append(result[opts.typesName], buf.Bytes()...)
					} else {
						result[opts.typesName+"_"+srcFileName] = append(result[opts.typesName+"_"+srcFileName], buf.Bytes()...)
					}
				}
			}

		}
	}

	return result, nil
}

// isStandardImportPath is copied from cmd/go in the standard library.
func isStandardImportPath(path string) bool {
	i := strings.Index(path, "/")
	if i < 0 {
		i = len(path)
	}
	elem := path[:i]
	return !strings.Contains(elem, ".")
}

func writeHeader(utilsBuffer *bytes.Buffer, newPackageName string, pkgStd, pkgExt map[string]bool) {
	utilsBuffer.Reset()
	if newPackageName == "type" {
		newPackageName = "types"
	}
	fmt.Fprintf(utilsBuffer, "package %s\n\n", newPackageName)
	fmt.Fprintln(utilsBuffer, "import (")
	for p := range pkgStd {
		fmt.Fprintf(utilsBuffer, "\t%s\n", p)
	}
	if len(pkgExt) > 0 {
		fmt.Fprintln(utilsBuffer)
	}
	for p := range pkgExt {
		fmt.Fprintf(utilsBuffer, "\t%s\n", p)
	}
	fmt.Fprint(utilsBuffer, ")\n\n")
}
