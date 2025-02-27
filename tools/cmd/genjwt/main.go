package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goccy/go-json"
	"github.com/goccy/go-yaml"
	"github.com/lestrrat-go/codegen"
)

const (
	byteSliceType = "[]byte"
)

func main() {
	if err := _main(); err != nil {
		log.Printf("%s", err)
		os.Exit(1)
	}
}

func _main() error {
	var objectsFile = flag.String("objects", "objects.yml", "")
	flag.Parse()
	jsonSrc, err := yaml2json(*objectsFile)
	if err != nil {
		return err
	}

	var def struct {
		CommonFields codegen.FieldList `json:"common_fields"`
		Objects      []*codegen.Object `json:"objects"`
	}
	if err := json.NewDecoder(bytes.NewReader(jsonSrc)).Decode(&def); err != nil {
		return fmt.Errorf(`failed to decode %q: %w`, *objectsFile, err)
	}

	for _, object := range def.Objects {
		for _, f := range def.CommonFields {
			object.AddField(f)
		}
		object.Organize()
	}

	for _, object := range def.Objects {
		if err := generateToken(object); err != nil {
			return fmt.Errorf(`failed to generate token file %s: %w`, objectFilename(object), err)
		}
	}

	for _, object := range def.Objects {
		if err := genBuilder(object); err != nil {
			return fmt.Errorf(`failed to generate builder for package %q: %w`, objectPackage(object), err)
		}
	}

	return nil
}

func boolFromField(f codegen.Field, field string) (bool, error) {
	v, ok := f.Extra(field)
	if !ok {
		return false, fmt.Errorf("%q does not exist in %q", field, f.Name(true))
	}

	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%q should be a bool in %q", field, f.Name(true))
	}
	return b, nil
}

func stringFromField(f codegen.Field, field string) (string, error) {
	v, ok := f.Extra(field)
	if !ok {
		return "", fmt.Errorf("%q does not exist in %q", field, f.Name(true))
	}

	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%q should be a string in %q", field, f.Name(true))
	}
	return s, nil
}

func fieldNoDeref(f codegen.Field) bool {
	v, _ := boolFromField(f, "noDeref")
	return v
}

func fieldHasGet(f codegen.Field) bool {
	v, _ := boolFromField(f, "hasGet")
	return v
}

func fieldHasAccept(f codegen.Field) bool {
	v, _ := boolFromField(f, "hasAccept")
	return v
}

func fieldGetterReturnValue(f codegen.Field) string {
	if v, err := stringFromField(f, `getter_return_value`); err == nil {
		return v
	}

	return f.Type()
}

func stringFromObject(o *codegen.Object, field string) (string, error) {
	v, ok := o.Extra(field)
	if !ok {
		return "", fmt.Errorf("%q does not exist in %q", field, o.Name(true))
	}

	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%q should be a string in %q", field, o.Name(true))
	}
	return s, nil
}

func objectFilename(o *codegen.Object) string {
	v, err := stringFromObject(o, `filename`)
	if err != nil {
		panic(err.Error())
	}
	return v
}

func objectPackage(o *codegen.Object) string {
	v, err := stringFromObject(o, `package`)
	if err != nil {
		panic(err.Error())
	}
	return v
}

func objectInterface(o *codegen.Object) string {
	v, err := stringFromObject(o, `interface`)
	if err != nil {
		panic(err.Error())
	}
	return v
}

func yaml2json(fn string) ([]byte, error) {
	in, err := os.Open(fn)
	if err != nil {
		return nil, fmt.Errorf(`failed to open %q: %w`, fn, err)
	}
	defer in.Close()

	var v interface{}
	if err := yaml.NewDecoder(in).Decode(&v); err != nil {
		return nil, fmt.Errorf(`failed to decode %q: %w`, fn, err)
	}

	return json.Marshal(v)
}

func IsPointer(f codegen.Field) bool {
	return strings.HasPrefix(f.Type(), `*`)
}

func PointerElem(f codegen.Field) string {
	return strings.TrimPrefix(f.Type(), `*`)
}

func fieldStorageType(s string) string {
	if fieldStorageTypeIsIndirect(s) {
		return `*` + s
	}
	return s
}

func fieldStorageTypeIsIndirect(s string) bool {
	return !(strings.HasPrefix(s, `*`) || strings.HasPrefix(s, `[]`) || strings.HasSuffix(s, `List`))
}

func generateToken(obj *codegen.Object) error {
	var buf bytes.Buffer

	o := codegen.NewOutput(&buf)
	o.L("// This file is auto-generated by jwt/internal/cmd/gentoken/main.go. DO NOT EDIT")
	o.LL("package %s", objectPackage(obj))

	var fields = obj.Fields()

	o.LL("const (")
	for _, f := range fields {
		o.L("%sKey = %s", f.Name(true), strconv.Quote(f.JSON()))
	}
	o.L(")") // end const

	if objectPackage(obj) == "jwt" && obj.Name(false) == "stdToken" {
		o.LL("// Token represents a generic JWT token.")
		o.L("// which are type-aware (to an extent). Other claims may be accessed via the `Get`/`Set`")
		o.L("// methods but their types are not taken into consideration at all. If you have non-standard")
		o.L("// claims that you must frequently access, consider creating accessors functions")
		o.L("// like the following")
		o.L("//\n// func SetFoo(tok jwt.Token) error")
		o.L("// func GetFoo(tok jwt.Token) (*Customtyp, error)")
		o.L("//\n// Embedding jwt.Token into another struct is not recommended, because")
		o.L("// jwt.Token needs to handle private claims, and this really does not")
		o.L("// work well when it is embedded in other structure")
	}

	o.L("type %s interface {", objectInterface(obj))
	for _, field := range fields {
		o.LL("// %s returns the value for %q field of the token", field.GetterMethod(true), field.JSON())
		o.L("%s() %s", field.GetterMethod(true), fieldGetterReturnValue(field))
	}
	o.LL("// PrivateClaims return the entire set of fields (claims) in the token")
	o.L("// *other* than the pre-defined fields such as `iss`, `nbf`, `iat`, etc.")
	o.L("PrivateClaims() map[string]interface{}")
	o.LL("// Get returns the value of the corresponding field in the token, such as")
	o.L("// `nbf`, `exp`, `iat`, and other user-defined fields. If the field does not")
	o.L("// exist in the token, the second return value will be `false`")
	o.L("//")
	o.L("// If you need to access fields like `alg`, `kid`, `jku`, etc, you need")
	o.L("// to access the corresponding fields in the JWS/JWE message. For this,")
	o.L("// you will need to access them by directly parsing the payload using")
	o.L("// `jws.Parse` and `jwe.Parse`")
	o.L("Get(string) (interface{}, bool)")

	o.LL("// Set assigns a value to the corresponding field in the token. Some")
	o.L("// pre-defined fields such as `nbf`, `iat`, `iss` need their values to")
	o.L("// be of a specific type. See the other getter methods in this interface")
	o.L("// for the types of each of these fields")
	o.L("Set(string, interface{}) error")
	o.L("Remove(string) error")
	if objectPackage(obj) != "jwt" {
		o.L("Clone() (jwt.Token, error)")
	} else {
		o.L("Clone() (Token, error)")
	}
	o.L("Iterate(context.Context) Iterator")
	o.L("Walk(context.Context, Visitor) error")
	o.L("AsMap(context.Context) (map[string]interface{}, error)")
	o.L("}")

	o.L("type %s struct {", obj.Name(false))
	o.L("mu *sync.RWMutex")
	o.L("dc DecodeCtx // per-object context for decoding")
	for _, f := range fields {
		if c := f.Comment(); c != "" {
			o.L("%s %s // %s", f.Name(false), fieldStorageType(f.Type()), c)
		} else {
			o.L("%s %s", f.Name(false), fieldStorageType(f.Type()))
		}
	}
	o.L("privateClaims map[string]interface{}")
	o.L("}") // end type Token

	o.LL("// New creates a standard token, with minimal knowledge of")
	o.L("// possible claims. Standard claims include")
	for i, field := range fields {
		o.R("%s", strconv.Quote(field.JSON()))
		switch {
		case i < len(fields)-2:
			o.R(", ")
		case i == len(fields)-2:
			o.R(" and ")
		}
	}

	o.R(".\n// Convenience accessors are provided for these standard claims")
	o.L("func New() %s {", objectInterface(obj))
	o.L("return &%s{", obj.Name(false))
	o.L("mu: &sync.RWMutex{},")
	o.L("privateClaims: make(map[string]interface{}),")
	o.L("}")
	o.L("}")

	o.LL("func (t *%s) Get(name string) (interface{}, bool) {", obj.Name(false))
	o.L("t.mu.RLock()")
	o.L("defer t.mu.RUnlock()")
	o.L("switch name {")
	for _, f := range fields {
		o.L("case %sKey:", f.Name(true))
		o.L("if t.%s == nil {", f.Name(false))
		o.L("return nil, false")
		o.L("}")
		if fieldHasGet(f) {
			o.L("v := t.%s.Get()", f.Name(false))
		} else {
			if fieldStorageTypeIsIndirect(f.Type()) {
				o.L("v := *(t.%s)", f.Name(false))
			} else {
				o.L("v := t.%s", f.Name(false))
			}
		}
		o.L("return v, true")
	}
	o.L("default:")
	o.L("v, ok := t.privateClaims[name]")
	o.L("return v, ok")
	o.L("}") // end switch name
	o.L("}") // end of Get

	o.LL("func (t *stdToken) Remove(key string) error {")
	o.L("t.mu.Lock()")
	o.L("defer t.mu.Unlock()")
	o.L("switch key {")
	for _, f := range fields {
		o.L("case %sKey:", f.Name(true))
		o.L("t.%s = nil", f.Name(false))
	}
	o.L("default:")
	o.L("delete(t.privateClaims, key)")
	o.L("}")
	o.L("return nil") // currently unused, but who knows
	o.L("}")

	o.LL("func (t *%s) Set(name string, value interface{}) error {", obj.Name(false))
	o.L("t.mu.Lock()")
	o.L("defer t.mu.Unlock()")
	o.L("return t.setNoLock(name, value)")
	o.L("}")

	o.LL("func (t *%s) DecodeCtx() DecodeCtx {", obj.Name(false))
	o.L("t.mu.RLock()")
	o.L("defer t.mu.RUnlock()")
	o.L("return t.dc")
	o.L("}")

	o.LL("func (t *%s) SetDecodeCtx(v DecodeCtx) {", obj.Name(false))
	o.L("t.mu.Lock()")
	o.L("defer t.mu.Unlock()")
	o.L("t.dc = v")
	o.L("}")

	o.LL("func (t *%s) setNoLock(name string, value interface{}) error {", obj.Name(false))
	o.L("switch name {")
	for _, f := range fields {
		keyName := f.Name(true) + "Key"
		o.L("case %s:", keyName)
		if f.Name(false) == `algorithm` {
			o.L("switch v := value.(type) {")
			o.L("case string:")
			o.L("t.algorithm = &v")
			o.L("case fmt.Stringer:")
			o.L("tmp := v.String()")
			o.L("t.algorithm = &tmp")
			o.L("default:")
			o.L("return fmt.Errorf(`invalid type for %%s key: %%T`, %s, value)", keyName)
			o.L("}")
			o.L("return nil")
		} else if fieldHasAccept(f) {
			if IsPointer(f) {
				o.L("var acceptor %s", strings.TrimPrefix(f.Type(), "*"))
			} else {
				o.L("var acceptor %s", f.Type())
			}

			o.L("if err := acceptor.Accept(value); err != nil {")
			o.L("return fmt.Errorf(`invalid value for %%s key: %%w`, %s, err)", keyName)
			o.L("}") // end if err := t.%s.Accept(value)
			if fieldStorageTypeIsIndirect(f.Type()) || IsPointer(f) {
				o.L("t.%s = &acceptor", f.Name(false))
			} else {
				o.L("t.%s = acceptor", f.Name(false))
			}
			o.L("return nil")
		} else {
			o.L("if v, ok := value.(%s); ok {", f.Type())
			if fieldStorageTypeIsIndirect(f.Type()) {
				o.L("t.%s = &v", f.Name(false))
			} else {
				o.L("t.%s = v", f.Name(false))
			}
			o.L("return nil")
			o.L("}") // end if v, ok := value.(%s)
			o.L("return fmt.Errorf(`invalid value for %%s key: %%T`, %s, value)", keyName)
		}
	}
	o.L("default:")
	o.L("if t.privateClaims == nil {")
	o.L("t.privateClaims = map[string]interface{}{}")
	o.L("}") // end if t.privateClaims == nil
	o.L("t.privateClaims[name] = value")
	o.L("}") // end switch name
	o.L("return nil")
	o.L("}") // end func (t *%s) Set(name string, value interface{})

	for _, f := range fields {
		o.LL("func (t *%s) %s() ", obj.Name(false), f.GetterMethod(true))
		if rv := fieldGetterReturnValue(f); rv != "" {
			o.R("%s", rv)
		} else if IsPointer(f) && fieldNoDeref(f) {
			o.R("%s", f.Type())
		} else {
			o.R("%s", PointerElem(f))
		}
		o.R(" {")
		o.L("t.mu.RLock()")
		o.L("defer t.mu.RUnlock()")

		if fieldHasGet(f) {
			o.L("if t.%s != nil {", f.Name(false))
			o.L("return t.%s.Get()", f.Name(false))
			o.L("}")
			o.L("return %s", codegen.ZeroVal(fieldGetterReturnValue(f)))
		} else if !IsPointer(f) {
			if fieldStorageTypeIsIndirect(f.Type()) {
				o.L("if t.%s != nil {", f.Name(false))
				o.L("return *(t.%s)", f.Name(false))
				o.L("}")
				o.L("return %s", codegen.ZeroVal(fieldGetterReturnValue(f)))
			} else {
				o.L("return t.%s", f.Name(false))
			}
		} else {
			o.L("return t.%s", f.Name(false))
		}
		o.L("}") // func (h *stdHeaders) %s() %s
	}

	o.LL("func (t *%s) PrivateClaims() map[string]interface{} {", obj.Name(false))
	o.L("t.mu.RLock()")
	o.L("defer t.mu.RUnlock()")
	o.L("return t.privateClaims")
	o.L("}")

	// Generate a function that iterates through all of the keys
	// in this header.
	o.LL("func (t *%s) makePairs() []*ClaimPair {", obj.Name(false))
	o.L("t.mu.RLock()")
	o.L("defer t.mu.RUnlock()")

	// NOTE: building up an array is *slow*?
	o.LL("pairs := make([]*ClaimPair, 0, %d)", len(fields))
	for _, f := range fields {
		keyName := f.Name(true) + "Key"
		o.L("if t.%s != nil {", f.Name(false))
		if fieldHasGet(f) {
			o.L("v := t.%s.Get()", f.Name(false))
		} else {
			if fieldStorageTypeIsIndirect(f.Type()) {
				o.L("v := *(t.%s)", f.Name(false))
			} else {
				o.L("v := t.%s", f.Name(false))
			}
		}
		o.L("pairs = append(pairs, &ClaimPair{Key: %s, Value: v})", keyName)
		o.L("}")
	}
	o.L("for k, v := range t.privateClaims {")
	o.L("pairs = append(pairs, &ClaimPair{Key: k, Value: v})")
	o.L("}")
	o.L("sort.Slice(pairs, func(i, j int) bool {")
	o.L("return pairs[i].Key.(string) < pairs[j].Key.(string)")
	o.L("})")
	o.L("return pairs")
	o.L("}") // end of (h *stdHeaders) iterate(...)

	o.LL("func (t *stdToken) UnmarshalJSON(buf []byte) error {")
	o.L("t.mu.Lock()")
	o.L("defer t.mu.Unlock()")
	for _, f := range fields {
		o.L("t.%s = nil", f.Name(false))
	}

	o.L("dec := json.NewDecoder(bytes.NewReader(buf))")
	o.L("LOOP:")
	o.L("for {")
	o.L("tok, err := dec.Token()")
	o.L("if err != nil {")
	o.L("return fmt.Errorf(`error reading token: %%w`, err)")
	o.L("}")
	o.L("switch tok := tok.(type) {")
	o.L("case json.Delim:")
	o.L("// Assuming we're doing everything correctly, we should ONLY")
	o.L("// get either '{' or '}' here.")
	o.L("if tok == '}' { // End of object")
	o.L("break LOOP")
	o.L("} else if tok != '{' {")
	o.L("return fmt.Errorf(`expected '{', but got '%%c'`, tok)")
	o.L("}")
	o.L("case string: // Objects can only have string keys")
	o.L("switch tok {")

	for _, f := range fields {
		if f.Type() == "string" {
			o.L("case %sKey:", f.Name(true))
			o.L("if err := json.AssignNextStringToken(&t.%s, dec); err != nil {", f.Name(false))
			o.L("return fmt.Errorf(`failed to decode value for key %%s: %%w`, %sKey, err)", f.Name(true))
			o.L("}")
		} else if f.Type() == byteSliceType {
			o.L("case %sKey:", f.Name(true))
			o.L("if err := json.AssignNextBytesToken(&t.%s, dec); err != nil {", f.Name(false))
			o.L("return fmt.Errorf(`failed to decode value for key %%s: %%w`, %sKey, err)", f.Name(true))
			o.L("}")
		} else if f.Type() == "types.StringList" || strings.HasPrefix(f.Type(), "[]") {
			o.L("case %sKey:", f.Name(true))
			o.L("var decoded %s", f.Type())
			o.L("if err := dec.Decode(&decoded); err != nil {")
			o.L("return fmt.Errorf(`failed to decode value for key %%s: %%w`, %sKey, err)", f.Name(true))
			o.L("}")
			o.L("t.%s = decoded", f.Name(false))
		} else {
			o.L("case %sKey:", f.Name(true))
			if IsPointer(f) {
				o.L("var decoded %s", PointerElem(f))
			} else {
				o.L("var decoded %s", f.Type())
			}
			o.L("if err := dec.Decode(&decoded); err != nil {")
			o.L("return fmt.Errorf(`failed to decode value for key %%s: %%w`, %sKey, err)", f.Name(true))
			o.L("}")
			o.L("t.%s = &decoded", f.Name(false))
		}
	}
	o.L("default:")
	// This looks like bad code, but we're unrolling things for maximum
	// runtime efficiency
	o.L("if dc := t.dc; dc != nil {")
	o.L("if localReg := dc.Registry(); localReg != nil {")
	o.L("decoded, err := localReg.Decode(dec, tok)")
	o.L("if err == nil {")
	o.L("t.setNoLock(tok, decoded)")
	o.L("continue")
	o.L("}")
	o.L("}")
	o.L("}")

	o.L("decoded, err := registry.Decode(dec, tok)")
	o.L("if err == nil {")
	o.L("t.setNoLock(tok, decoded)")
	o.L("continue")
	o.L("}")
	o.L("return fmt.Errorf(`could not decode field %%s: %%w`, tok, err)")
	o.L("}")
	o.L("default:")
	o.L("return fmt.Errorf(`invalid token %%T`, tok)")
	o.L("}")
	o.L("}")

	o.L("return nil")
	o.L("}")

	var numericDateFields []codegen.Field
	for _, field := range fields {
		if field.Type() == "types.NumericDate" {
			numericDateFields = append(numericDateFields, field)
		}
	}

	o.LL("func (t %s) MarshalJSON() ([]byte, error) {", obj.Name(false))
	o.L("t.mu.RLock()")
	o.L("defer t.mu.RUnlock()")
	o.L("buf := pool.GetBytesBuffer()")
	o.L("defer pool.ReleaseBytesBuffer(buf)")
	o.L("buf.WriteByte('{')")
	o.L("enc := json.NewEncoder(buf)")
	o.L("for i, pair := range t.makePairs() {")
	o.L("f := pair.Key.(string)")
	o.L("if i > 0 {")
	o.L("buf.WriteByte(',')")
	o.L("}")
	o.L("buf.WriteRune('\"')")
	o.L("buf.WriteString(f)")
	o.L("buf.WriteString(`\":`)")

	// Handle cases that need specialized handling
	o.L("switch f {")
	o.L("case AudienceKey:")
	o.L("if err := json.EncodeAudience(enc, pair.Value.([]string)); err != nil {")
	o.L("return nil, fmt.Errorf(`failed to encode \"aud\": %%w`, err)")
	o.L("}")
	o.L("continue")
	if lndf := len(numericDateFields); lndf > 0 {
		o.L("case ")
		for i, ndf := range numericDateFields {
			o.R("%sKey", ndf.Name(true))
			if i < lndf-1 {
				o.R(",")
			}
		}
		o.R(":")
		o.L("enc.Encode(pair.Value.(time.Time).Unix())")
		o.L("continue")
	}
	o.L("}")

	o.L("switch v := pair.Value.(type) {")
	o.L("case []byte:")
	o.L("buf.WriteRune('\"')")
	o.L("buf.WriteString(base64.EncodeToString(v))")
	o.L("buf.WriteRune('\"')")
	o.L("default:")
	o.L("if err := enc.Encode(v); err != nil {")
	o.L("return nil, fmt.Errorf(`failed to marshal field %%s: %%w`, f, err)")
	o.L("}")
	o.L("buf.Truncate(buf.Len()-1)")
	o.L("}")
	o.L("}")
	o.L("buf.WriteByte('}')")
	o.L("ret := make([]byte, buf.Len())")
	o.L("copy(ret, buf.Bytes())")
	o.L("return ret, nil")
	o.L("}")

	o.LL("func (t *%s) Iterate(ctx context.Context) Iterator {", obj.Name(false))
	o.L("pairs := t.makePairs()")
	o.L("ch := make(chan *ClaimPair, len(pairs))")
	o.L("go func(ctx context.Context, ch chan *ClaimPair, pairs []*ClaimPair) {")
	o.L("defer close(ch)")
	o.L("for _, pair := range pairs {")
	o.L("select {")
	o.L("case <-ctx.Done():")
	o.L("return")
	o.L("case ch<-pair:")
	o.L("}")
	o.L("}")
	o.L("}(ctx, ch, pairs)")
	o.L("return mapiter.New(ch)")
	o.L("}")

	o.LL("func (t *%s) Walk(ctx context.Context, visitor Visitor) error {", obj.Name(false))
	o.L("return iter.WalkMap(ctx, t, visitor)")
	o.L("}")

	o.LL("func (t *%s) AsMap(ctx context.Context) (map[string]interface{}, error) {", obj.Name(false))
	o.L("return iter.AsMap(ctx, t)")
	o.L("}")

	if err := o.WriteFile(objectFilename(obj), codegen.WithFormatCode(true)); err != nil {
		if cfe, ok := err.(codegen.CodeFormatError); ok {
			fmt.Fprint(os.Stderr, cfe.Source())
		}
		return fmt.Errorf(`failed to write to %s: %w`, objectFilename(obj), err)
	}
	return nil
}

func genBuilder(obj *codegen.Object) error {
	var buf bytes.Buffer
	pkg := objectPackage(obj)
	o := codegen.NewOutput(&buf)
	o.L("// This file is auto-generated by jwt/internal/cmd/gentoken/main.go. DO NOT EDIT")
	o.LL("package %s", pkg)

	o.LL("// Builder is a convenience wrapper around the New() constructor")
	o.L("// and the Set() methods to assign values to Token claims.")
	o.L("// Users can successively call Claim() on the Builder, and have it")
	o.L("// construct the Token when Build() is called. This alleviates the")
	o.L("// need for the user to check for the return value of every single")
	o.L("// Set() method call.")
	o.L("// Note that each call to Claim() overwrites the value set from the")
	o.L("// previous call.")
	o.L("type Builder struct {")
	o.L("claims []*ClaimPair")
	o.L("}")

	o.LL("func NewBuilder() *Builder {")
	o.L("return &Builder{}")
	o.L("}")

	o.LL("func (b *Builder) Claim(name string, value interface{}) *Builder {")
	o.L("b.claims = append(b.claims, &ClaimPair{Key: name, Value: value})")
	o.L("return b")
	o.L("}")

	for _, f := range obj.Fields() {
		ftyp := f.Type()
		if ftyp == "types.NumericDate" {
			ftyp = "time.Time"
		} else if ftyp == "types.StringList" {
			ftyp = "[]string"
		}
		o.LL("func (b *Builder) %s(v %s) *Builder {", f.Name(true), ftyp)
		o.L("return b.Claim(%sKey, v)", f.Name(true))
		o.L("}")
	}

	o.LL("// Build creates a new token based on the claims that the builder has received")
	o.L("// so far. If a claim cannot be set, then the method returns a nil Token with")
	o.L("// a en error as a second return value")
	o.L("func (b *Builder) Build() (Token, error) {")
	o.L("tok := New()")
	o.L("for _, claim := range b.claims {")
	o.L("if err := tok.Set(claim.Key.(string), claim.Value); err != nil {")
	o.L("return nil, fmt.Errorf(`failed to set claim %%q: %%w`, claim.Key.(string), err)")
	o.L("}")
	o.L("}")
	o.L("return tok, nil")
	o.L("}")

	fn := "builder_gen.go"
	if pkg != "jwt" {
		fn = filepath.Join(pkg, fn)
	}
	if err := o.WriteFile(fn, codegen.WithFormatCode(true)); err != nil {
		if cfe, ok := err.(codegen.CodeFormatError); ok {
			fmt.Fprint(os.Stderr, cfe.Source())
		}
		return fmt.Errorf(`failed to write to %s: %w`, fn, err)
	}
	return nil
}
