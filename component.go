package hvue

import "github.com/gopherjs/gopherjs/js"

// NewComponent defines a new Vue component.  It wraps js{Vue.component}:
// https://vuejs.org/v2/api/#Vue-component.
func NewComponent(name string, opts ...ComponentOption) {
	c := &Config{Object: o()}
	c.Setters = o()
	c.Option(opts...)

	if c.Data == js.Undefined {
		c.Object.Set("data", jsCallWithVM(func(vm *VM) interface{} {
			obj := o()
			// Get the parent data object ID, if it exists
			dataID := vm.Get("$parent").Get("$data").Get("hvue_dataID")
			if dataID != js.Undefined {
				obj.Set("hvue_dataID", dataID)
			}
			return obj
		}))
	}

	js.Global.Get("Vue").Call("component", name, c.Object)
}

// Component is used in NewVM to define a local component, within the scope of
// another instance/component.
// https://vuejs.org/v2/guide/components.html#Local-Registration
func Component(name string, opts ...ComponentOption) ComponentOption {
	return func(c *Config) {
		componentOption := &Config{Object: o()}
		componentOption.Option(opts...)

		if c.Components == js.Undefined {
			c.Components = o()
		}

		c.Components.Set(name, componentOption.Object)
	}
}

// Props defines one or more simple prop slots.  For complex prop slots, use
// PropObj().  https://vuejs.org/v2/api/#props
func Props(props ...string) ComponentOption {
	return func(c *Config) {
		if c.Props == js.Undefined {
			c.Props = NewArray()
		}
		for i, prop := range props {
			c.Props.SetIndex(i, prop)
		}
	}
}

// PropObj defines a complex prop slot called `name`, configured with Types,
// Default, DefaultFunc, and Validator.
func PropObj(name string, opts ...PropOption) ComponentOption {
	return func(c *Config) {
		if c.Props == js.Undefined {
			c.Props = o()
		}
		pO := &PropConfig{Object: o()}
		pO.Option(opts...)
		c.Props.Set(name, pO.Object)
	}
}

// Template defines a template for a component.  It sets the js{template} slot
// of a js{Vue.component}'s configuration object.
func Template(template string) ComponentOption {
	return func(c *Config) {
		c.Template = template
	}
}

// Types configures the allowed types for a prop.
// https://vuejs.org/v2/guide/components.html#Props.
func Types(types ...pOptionType) PropOption {
	return func(p *PropConfig) {
		arr := js.Global.Get("Array").New()
		for _, t := range types {
			var newVal *js.Object
			switch t {
			case PString:
				newVal = js.Global.Get("String")
			case PNumber:
				newVal = js.Global.Get("Number")
			case PBoolean:
				newVal = js.Global.Get("Boolean")
			case PFunction:
				newVal = js.Global.Get("Function")
			case PObject:
				newVal = js.Global.Get("Object")
			case PArray:
				newVal = js.Global.Get("Array")
			}
			arr.Call("push", newVal)
		}
		p.typ = arr
	}
}

// Required specifies that the prop is required.
// https://vuejs.org/v2/guide/components.html#Props.
var Required PropOption = func(p *PropConfig) {
	p.required = true
}

// Default gives the default for a prop.
// https://vuejs.org/v2/guide/components.html#Props
func Default(def interface{}) PropOption {
	return func(p *PropConfig) {
		p.def = def
	}
}

// DefaultFunc sets a function that returns the default for a prop.
// https://vuejs.org/v2/guide/components.html#Props
func DefaultFunc(def func(*VM) interface{}) PropOption {
	return func(p *PropConfig) {
		p.def = jsCallWithVM(def)
	}
}

// Validator functions generate warnings in the JS console if using the
// vue.js development build.  They don't panic or otherwise crash your code,
// they just give warnings if the validation fails.
func Validator(f func(vm *VM, value *js.Object) interface{}) PropOption {
	return func(p *PropConfig) {
		p.validator = js.MakeFunc(
			func(this *js.Object, args []*js.Object) interface{} {
				vm := &VM{Object: this}
				return f(vm, args[0])
			})
	}
}
