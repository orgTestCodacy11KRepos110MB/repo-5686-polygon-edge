package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io/ioutil"
	"strconv"
	"strings"
	"text/template"

	gensc "github.com/0xPolygon/polygon-edge/consensus/polybft/contractsapi"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/contractsapi/artifact"
	"github.com/umbracle/ethgo/abi"
)

const (
	contractVariableName   = "%sContract"
	contractStructName     = "%sContractImpl"
	contractVariableFormat = "%s = &%s{Artifact: %s}"
	abiTypeNameFormat      = "var %sABIType = abi.MustNewType(\"%s\")"
	eventNameFormat        = "%sEvent"
	functionNameFormat     = "%sFunction"
)

func main() {
	cases := []struct {
		contractName string
		artifact     *artifact.Artifact
		functions    []string
		events       []string
	}{
		{
			"StateReceiver",
			gensc.StateReceiver,
			[]string{
				"commit",
				"execute",
			},
			[]string{
				"StateSyncResult",
				"NewCommitment",
			},
		},
		{
			"ChildValidatorSet",
			gensc.ChildValidatorSet,
			[]string{
				"commitEpoch",
			},
			[]string{},
		},
		{
			"StateSender",
			gensc.StateSender,
			[]string{
				"syncState",
			},
			[]string{
				"StateSynced",
			},
		},
		{
			"CheckpointManager",
			gensc.CheckpointManager,
			[]string{
				"submit",
			},
			[]string{},
		},
	}

	rr := render{}
	res := []string{}

	for _, c := range cases {
		for _, method := range c.functions {
			res = append(res, rr.GenMethod(c.contractName, c.artifact.Abi.Methods[method]))
		}

		for _, event := range c.events {
			res = append(res, rr.GenEvent(c.contractName, c.artifact.Abi.Events[event]))
		}
	}

	str := `// Code generated by scapi/gen. DO NOT EDIT.
package contractsapi

import (
	"math/big"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/umbracle/ethgo/abi"
	"github.com/umbracle/ethgo"
)

`
	str += strings.Join(res, "\n")

	output, err := format.Source([]byte(str))
	if err != nil {
		fmt.Println(str)
		panic(err)
	}

	if err := ioutil.WriteFile("./consensus/polybft/contractsapi/contractsapi.go", output, 0600); err != nil {
		panic(err)
	}
}

func getInternalType(paramName string, paramAbiType *abi.Type) string {
	internalType := paramAbiType.InternalType()
	if internalType == "" {
		internalType = strings.Title(paramName)
	} else {
		internalType = strings.TrimSuffix(internalType, "[]")      // remove [] if it's struct array
		internalType = strings.TrimPrefix(internalType, "struct ") // remove struct prefix
		// if struct is taken from an interface (ICheckpoint.Validator), remove interface
		parts := strings.Split(internalType, ".")
		if len(parts) > 1 {
			internalType = parts[1]
		}
	}

	return internalType
}

func genType(name string, obj *abi.Type, res *[]string) string {
	if obj.Kind() != abi.KindTuple {
		panic("BUG: Not expected")
	}

	internalType := getInternalType(name, obj)
	str := []string{
		"type " + internalType + " struct {",
	}

	for _, tupleElem := range obj.TupleElems() {
		elem := tupleElem.Elem

		var typ string

		if elem.Kind() == abi.KindTuple {
			// Struct
			typ = genNestedType(tupleElem.Name, elem, res)
		} else if elem.Kind() == abi.KindSlice && elem.Elem().Kind() == abi.KindTuple {
			// []Struct
			typ = "[]" + genNestedType(getInternalType(tupleElem.Name, elem), elem.Elem(), res)
		} else if elem.Kind() == abi.KindArray && elem.Elem().Kind() == abi.KindTuple {
			// [n]Struct
			typ = "[" + strconv.Itoa(elem.Size()) + "]" + genNestedType(getInternalType(tupleElem.Name, elem), elem.Elem(), res)
		} else if elem.Kind() == abi.KindAddress {
			// for address use the native `types.Address` type instead of `ethgo.Address`. Note that
			// this only works for simple types and not for []address inputs. This is good enough since
			// there are no kinds like that in our smart contracts.
			typ = "types.Address"
		} else {
			// for the rest of the types use the go type returned by abi
			typ = elem.GoType().String()
		}

		// []byte and [n]byte get rendered as []uint68 and [n]uint8, since we do not have any
		// uint8 internally in polybft, we can use regexp to replace those values with the
		// correct byte representation
		typ = strings.Replace(typ, "[32]uint8", "types.Hash", -1)
		typ = strings.Replace(typ, "]uint8", "]byte", -1)

		// Trim the leading _ from name if it exists
		fieldName := strings.TrimPrefix(tupleElem.Name, "_")

		// Replacement of Id for ID to make the linter happy
		fieldName = strings.Title(fieldName)
		fieldName = strings.Replace(fieldName, "Id", "ID", -1)

		str = append(str, fmt.Sprintf("%s %s `abi:\"%s\"`", fieldName, typ, tupleElem.Name))
	}

	str = append(str, "}")
	*res = append(*res, strings.Join(str, "\n"))

	return internalType
}

func genNestedType(name string, obj *abi.Type, res *[]string) string {
	result := genType(name, obj, res)
	*res = append(*res, fmt.Sprintf(abiTypeNameFormat, result, obj.Format(true)))
	*res = append(*res, genAbiFuncsForNestedType(result))

	return "*" + result
}

func genAbiFuncsForNestedType(name string) string {
	tmpl := `func ({{.Sig}} *{{.TName}}) EncodeAbi() ([]byte, error) {
		return {{.Name}}ABIType.Encode({{.Sig}})
	}
	
	func ({{.Sig}} *{{.TName}}) DecodeAbi(buf []byte) error {
		return decodeStruct({{.Name}}ABIType, buf, &{{.Sig}})
	}`

	title := strings.Title(name)

	inputs := map[string]interface{}{
		"Sig":   string(name[0]),
		"Name":  title,
		"TName": title,
	}

	return renderTmpl(tmpl, inputs)
}

type render struct {
}

func (r *render) GenEvent(contractName string, event *abi.Event) string {
	name := fmt.Sprintf(eventNameFormat, event.Name)

	res := []string{}
	genType(name, event.Inputs, &res)

	// write encode/decode functions
	tmplStr := `
{{range .Structs}}
	{{.}}
{{ end }}

func ({{.Sig}} *{{.TName}}) ParseLog(log *ethgo.Log) error {
	return decodeEvent({{.ContractName}}.Abi.Events["{{.Name}}"], log, {{.Sig}})
}`

	inputs := map[string]interface{}{
		"Structs":      res,
		"Sig":          strings.ToLower(string(name[0])),
		"Name":         event.Name,
		"TName":        strings.Title(name),
		"ContractName": contractName,
	}

	return renderTmpl(tmplStr, inputs)
}

func (r *render) GenMethod(contractName string, method *abi.Method) string {
	methodName := fmt.Sprintf(functionNameFormat, method.Name)

	res := []string{}
	genType(methodName, method.Inputs, &res)

	// write encode/decode functions
	tmplStr := `
{{range .Structs}}
	{{.}}
{{ end }}

func ({{.Sig}} *{{.TName}}) EncodeAbi() ([]byte, error) {
	return {{.ContractName}}.Abi.Methods["{{.Name}}"].Encode({{.Sig}})
}

func ({{.Sig}} *{{.TName}}) DecodeAbi(buf []byte) error {
	return decodeMethod({{.ContractName}}.Abi.Methods["{{.Name}}"], buf, {{.Sig}})
}`

	methodType := "function " + method.Name + "("
	if len(method.Inputs.TupleElems()) != 0 {
		methodType += encodeFuncTuple(method.Inputs)
	}

	methodType += ")"

	if len(method.Outputs.TupleElems()) != 0 {
		methodType += "(" + encodeFuncTuple(method.Outputs) + ")"
	}

	inputs := map[string]interface{}{
		"Structs":      res,
		"Type":         methodType,
		"Sig":          string(methodName[0]),
		"Name":         method.Name,
		"ContractName": contractName,
		"TName":        strings.Title(methodName),
	}

	return renderTmpl(tmplStr, inputs)
}

func renderTmpl(tmplStr string, inputs map[string]interface{}) string {
	tmpl, err := template.New("name").Parse(tmplStr)
	if err != nil {
		panic(fmt.Sprintf("BUG: Failed to load template: %v", err))
	}

	var tpl bytes.Buffer
	if err = tmpl.Execute(&tpl, inputs); err != nil {
		panic(fmt.Sprintf("BUG: Failed to render template: %v", err))
	}

	return tpl.String()
}

func encodeFuncTuple(t *abi.Type) string {
	if t.Kind() != abi.KindTuple {
		panic("BUG: Kind different than tuple not expected")
	}

	str := t.Format(true)
	str = strings.TrimPrefix(str, "tuple(")
	str = strings.TrimSuffix(str, ")")

	return str
}