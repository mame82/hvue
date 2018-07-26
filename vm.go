package hvue

import (
	"reflect"

	"github.com/gopherjs/gopherjs/js"
)

var o = func() *js.Object { return js.Global.Get("Object").New() }

// VM wraps a js Vue object.
type VM struct {
	*js.Object
	Data  *js.Object `js:"$data"`
	Props *js.Object `js:"$props"`
	El    *js.Object `js:"$el"`

	// Several of these should probably be functions, like Refs already is.
	Options     *js.Object   `js:"$options"`
	Parent      *js.Object   `js:"$parent"`
	Root        *js.Object   `js:"$root"`
	Children    []*js.Object `js:"$children"`
	Slots       *js.Object   `js:"$slots"`
	ScopedSlots *js.Object   `js:"$scopedSlots"`
	IsServer    bool         `js:"$isServer"`

	Store       *js.Object   `js:"$store"` //Vuex store, in case option is enabled, see https://vuex.vuejs.org/guide/state.html

	// Note existence of fields with setter methods, which won't show up in
	// $data.
	Setters *js.Object `js:"hvue_setters"`
}

var (
	jsOType     = reflect.TypeOf(o())
	vmType      = reflect.TypeOf(&VM{})
	dataObjects = map[int]interface{}{}
	nextDataID  = 1
)

// NewVM returns a new vm, analogous to Javascript `new Vue(...)`.  See
// https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis and
// https://commandcenter.blogspot.com.au/2014/01/self-referential-functions-and-design.html
// for discussions of how the options work, and also see the examples tree.
//
// If you use a data object (via DataS) and it has a VM field, it's set to
// this new VM.  TODO: Verify that the VM field is of type *hvue.VM.
func NewVM(opts ...ComponentOption) *VM {
	c := &Config{Object: NewObject()}
	c.Setters = NewObject()
	c.Option(opts...)
	vm := &VM{Object: js.Global.Get("Vue").New(c)}
	if c.dataValue.IsValid() {
		if vmField := c.dataValue.FieldByName("VM"); vmField.IsValid() {
			vmField.Set(reflect.ValueOf(vm))
		}
	}
	vm.Setters = c.Setters
	return vm
}

// El sets the vm's el slot.
func El(selector string) ComponentOption {
	return func(c *Config) {
		c.El = selector
	}
}

// Store enables the Vuex store option, as described here: https://vuex.vuejs.org/guide/state.html
func Store() ComponentOption {
	return func(c *Config) {
		// Note: The Vuex store has to be already exposed to js.Global "store" with this name, the original
		// implementation doesn't allow to provide another name
		c.Store = js.Global.Get("store")
	}
}

// Data sets a single data field.  Data can be called multiple times for the
// same vm.
//
// FIXME: You can't use MethodsOf with this function.
func Data(name string, value interface{}) ComponentOption {
	return func(c *Config) {
		if c.Data == js.Undefined {
			c.Data = NewObject()
		}
		c.Data.Set(name, value)
	}
}

// DataS sets the struct `value` as the entire contents of the vm's data
// field.  `value` should be a pointer to the struct.  If the object has a VM
// field, NewVM sets it to the new VM object.
func DataS(value interface{}) ComponentOption {
	return func(c *Config) {
		if c.Data != js.Undefined {
			panic("Cannot use hvue.DataS together with any other Data* options")
		}
		// Can't say `c.Data = value` because c.Data is a *js.Object, and value
		// is an interface{}.  Its underlying type must be a pointer to a
		// js-special struct, but we can't get at that struct's Object field
		// without a bunch of reflection, so take this shortcut.
		c.Object.Set("data", value)
		c.dataValue = reflect.ValueOf(value).Elem()
		storeDataID(c.Object.Get("data"), value, c)
	}
}

// DataFunc defines a function that returns a new data object.  You have to
// use DataFunc with Components, not Data or DataS.
//
// Note that this function is called when the VM or component is created
// (https://vuejs.org/v2/api/#created), not when you call "NewVM".  This means
// that you can't, for example, get clever and try to use the same object here
// as with MethodsOf.  MethodsOf requires an object when you call NewVM to
// reguster the VM, long before the VM is actually created or bound; this is
// called every time a new VM or component is created.
func DataFunc(f func(*VM) interface{}) ComponentOption {
	return func(c *Config) {
		if c.Data != js.Undefined {
			panic("Cannot use hvue.DataFunc together with any other Data* options")
		}
		// See comment about c.Data in DataS().
		c.Object.Set("data", jsCallWithVM(func(vm *VM) interface{} {
			// Get the new data object
			value := f(vm)

			// Find the *js.Object in field 0, however deep.
			// FIXME: If the types are wrong at any point (not pointer to a
			// struct at each level), then this'll fail with a
			// probably-not-very-clear error message.
			i := reflect.ValueOf(value).Elem().Field(0)
			for i.Type() != jsOType {
				i = i.Elem().Field(0)
			}
			storeDataID(i.Interface().(*js.Object), value, c)
			return value
		}))
	}
}

// Store a data object ID in the data object, for later reference.
//
// This wouldn't work if the *js.Object is sealed or not "plain" (like
// WebSocket).  But on the other hand, Vue won't work with non-plain or sealed
// objects, so it doesn't matter.
func storeDataID(o *js.Object, value interface{}, c *Config) {
	curID := nextDataID // small race condition here
	nextDataID++
	o.Set("hvue_dataID", curID)

	// Store the Go data object, indexed by curID
	dataObjects[curID] = value

	// Schedule it to be deleted when the vm is deleted
	Destroyed(func(*VM) {
		delete(dataObjects, curID)
	})(c)

}

// Method adds a single function as a "method" on a vm.  It does not change
// the method set of the data object, if any.
func Method(name string, f interface{}) ComponentOption {
	return func(c *Config) {
		if c.Methods == js.Undefined {
			c.Methods = NewObject()
		}
		m := reflect.ValueOf(f)
		if m.Kind() != reflect.Func {
			panic("Method " + name + " is not a func")
		}

		c.Methods.Set(name,
			makeMethod(name, false, m.Type(), m))
	}
}

// MethodsOf sets up vm.methods with the exported methods of the type that t
// is an instance of.  Call it like MethodsOf(&SomeType{}).  SomeType must be
// a pure Javascript object, with no Go fields.  That is, all slots just have
// `js:"..."` tags.
//
// If a method wants a pointer to its vm, use a *VM as the first argument.
//
// You can't use MethodsOf with Data(), only with DataS or DataFunc().
func MethodsOf(t interface{}) ComponentOption {
	return func(c *Config) {
		if c.Methods == js.Undefined {
			c.Methods = NewObject()
		}
		typ := reflect.TypeOf(t)
		if typ.Kind() != reflect.Ptr ||
			typ.Elem().Kind() != reflect.Struct {
			panic("Item passed to MethodsOf must be a pointer to a struct")
		}

		// Loop through all methods of the type
		for i := 0; i < typ.NumMethod(); i++ {
			m := typ.Method(i)
			c.Methods.Set(m.Name,
				makeMethod(m.Name, true, m.Type, m.Func))
		}
	}
}

func makeMethod(name string, isMethod bool, mType reflect.Type, m reflect.Value) *js.Object {
	return js.MakeFunc(
		func(this *js.Object, jsArgs []*js.Object) interface{} {
			// Construct the arglist
			numIn := mType.NumIn()
			goArgs := make([]reflect.Value, numIn)
			goArg := 0

			if isMethod {
				// Lookup the receiver in dataObjects, based on
				// $data.hvue_dataID
				dataID := this.Get("$data").Get("hvue_dataID").Int()
				if dataID == 0 {
					// FIXME: A better error here would be great, Mmmkay?
					panic("Unknown dataID for method " + name)
				}
				receiver, ok := dataObjects[dataID]
				if !ok {
					panic("Unknown dataID for method " + name)
				}

				goArgs[0] = reflect.ValueOf(receiver)
				goArg = 1
			}

			vmDone := false
			// We say || in the WHILE clause instead of && because there could be
			// Go args (like the receiver and a *VM arg) that wouldn't show up in
			// the JS arglist.
			for jsArg := 0; jsArg < len(jsArgs) || goArg < numIn; goArg, jsArg = goArg+1, jsArg+1 {
				if goArg >= numIn {
					break
				}
				switch mType.In(goArg).Kind() {
				case reflect.Ptr:
					inPtrType := mType.In(goArg)
					switch inPtrType {
					case jsOType:
						// A *js.Object
						goArgs[goArg] = reflect.ValueOf(jsArgs[jsArg])
					case vmType:
						// A *VM
						if vmDone {
							panic("Only a single *hvue.VM arg expected per method: " + name)
						}
						goArgs[goArg] = reflect.ValueOf(&VM{Object: this})
						jsArg--
						vmDone = true
					default:
						// Expects a pointer to a struct with first field
						// of type *js.Object.  Doesn't work yet with nested
						// structs.
						inType := inPtrType.Elem()
						inArg := reflect.New(inType)
						inArg.Elem().Field(0).Set(reflect.ValueOf(jsArgs[jsArg]))
						goArgs[goArg] = inArg
					}
				case reflect.String:
					goArgs[goArg] = reflect.ValueOf(jsArgs[jsArg].String())
				case reflect.Bool:
					goArgs[goArg] = reflect.ValueOf(jsArgs[jsArg].Bool())
				case reflect.Float64:
					goArgs[goArg] = reflect.ValueOf(jsArgs[jsArg].Float())
				case reflect.Int32, reflect.Int:
					goArgs[goArg] = reflect.ValueOf(jsArgs[jsArg].Int())
				case reflect.Int64:
					goArgs[goArg] = reflect.ValueOf(jsArgs[jsArg].Int64())
				default:
					panic("Unknown type in arglist for " +
						name + ": " + mType.In(goArg).Kind().String())
				}
			}

			result := m.Call(goArgs)

			// I don't think method results are ever actually used, but
			// I could be wrong.
			if len(result) >= 1 {
				return result[0].Interface()
			}
			return nil
		})
}

// untested
func Filter(name string, f func(vm *VM, value *js.Object, args ...*js.Object) interface{}) ComponentOption {
	return func(c *Config) {
		if c.Filters == js.Undefined {
			c.Filters = o()
		}

		c.Filters.Set(name, js.MakeFunc(
			func(this *js.Object, args []*js.Object) interface{} {
				vm := &VM{Object: this}
				return f(vm, args[0], args[1:]...)
			}))
	}
}

// Emit emits an event.  It wraps js{vm.$emit}:
// https://vuejs.org/v2/api/#vm-emit.
func (vm *VM) Emit(event string, args ...interface{}) {
	args = append([]interface{}{event}, args...)
	vm.Call("$emit", args...)
}

// Refs returns the ref for name.  vm.Refs("foo") compiles to
// js{vm.$refs.foo}.  It wraps vm.$refs: https://vuejs.org/v2/api/#vm-refs.
func (vm *VM) Refs(name string) *js.Object {
	return vm.Get("$refs").Get(name)
}

// GetData returns the Go data object associated with a *VM.  You need to type
// assert its return value to data type you passed to DataS(), or returned
// from the function given to DataFunc().
func (vm *VM) GetData() interface{} {
	dataID := vm.Data.Get("hvue_dataID").Int()
	if dataID == 0 {
		// FIXME: A better error here would be great, Mmmkay?
		panic("Unknown dataID in GetData")
	}
	dataObj, ok := dataObjects[dataID]
	if !ok {
		panic("Unknown dataID in GetData")
	}
	return dataObj
}

// Set wraps (*js.Object).Set(), but checks to make sure it's a valid slot in
// the VM's data object, and panics otherwise.  (If you don't want this check,
// then use vm.Object.Set() directly.)
func (vm *VM) Set(key string, value interface{}) {
	if vm.Object.Get("$data").Get(key) == js.Undefined &&
		vm.Setters.Get(key) == js.Undefined {
		panic("Unknown data slot set: " + key)
	}
	vm.Object.Set(key, value)
}
