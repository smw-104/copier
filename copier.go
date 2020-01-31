package copier

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
)

// Copy copy things
func Copy(toValue interface{}, fromValue interface{}) (err error) {
	var (
		isSlice bool
		amount  = 1
		from    = indirect(reflect.ValueOf(fromValue))
		to      = indirect(reflect.ValueOf(toValue))
	)

	if !to.CanAddr() {
		return errors.New("copy to value is unaddressable")
	}

	// Return is from value is invalid
	if !from.IsValid() {
		return
	}

	fromType := indirectType(from.Type())
	toType := indirectType(to.Type())

	// Just set it if possible to assign
	// And need to do copy anyway if the type is struct
	if fromType.Kind() != reflect.Struct && from.Type().AssignableTo(to.Type()) {
		to.Set(from)
		return
	}

	if fromType.Kind() != reflect.Struct || toType.Kind() != reflect.Struct {
		return
	}

	if to.Kind() == reflect.Slice {
		isSlice = true
		if from.Kind() == reflect.Slice {
			amount = from.Len()
		}
	}

	for i := 0; i < amount; i++ {
		var dest, source reflect.Value

		//srcFieldValue := srcValue.FieldByName(f)
		//srcFieldType, srcFieldFound := srcValue.Type().FieldByName(f)
		//srcFieldName := srcFieldType.Name
		//dstFieldName := srcFieldName

		if isSlice {
			// source
			if from.Kind() == reflect.Slice {
				source = indirect(from.Index(i))
			} else {
				source = indirect(from)
			}
			// dest
			dest = indirect(reflect.New(toType).Elem())
		} else {
			source = indirect(from)
			dest = indirect(to)
		}

		// Valuer -> ptr
		if source.IsValid() {
			fromTypeFields := deepFields(fromType)
			//fmt.Printf("%#v", fromTypeFields)
			// Copy from field to field or method
			for _, field := range fromTypeFields {
				name := field.Name

				if fromField := source.FieldByName(name); fromField.IsValid() {
					// has field
					if toField := dest.FieldByName(name); toField.IsValid() {
						if isNullableType(fromField.Type()) && toField.Kind() == reflect.Ptr {
							// We have same nullable type on both sides
							if fromField.Type().AssignableTo(toField.Type()) {
								toField.Set(fromField)
								continue
							}

							v, _ := fromField.Interface().(driver.Valuer).Value()
							if v == nil {
								if toField.Kind() == reflect.Ptr {
									pf := reflect.New(toField.Type().Elem())
									if pf.Elem().Kind() == reflect.Ptr {
										toField.Set(pf)
									}
								}
								continue
							}

							valueType := reflect.TypeOf(v)

							ptr := reflect.New(valueType)
							ptr.Elem().Set(reflect.ValueOf(v))

							assignableToField := toField
							assignableFieldType := assignableToField.Type()
							previousAssignableToField := assignableToField
							for assignableToField.Kind() == reflect.Ptr {
								previousAssignableToField = assignableToField
								assignableToField.Set(reflect.New(assignableToField.Type().Elem()))
								assignableToField = reflect.Indirect(assignableToField)
								assignableFieldType = assignableFieldType.Elem()
							}

							if valueType.AssignableTo(assignableFieldType) { //toField.Type().Elem()
								previousAssignableToField.Set(ptr)
							}

							continue
						} else if isNullableType(fromField.Type()) {
							// We have same nullable type on both sides
							if fromField.Type().AssignableTo(toField.Type()) {
								toField.Set(fromField)
								continue
							}

							v, _ := fromField.Interface().(driver.Valuer).Value()
							if v == nil {
								continue
							}

							rv := reflect.ValueOf(v)
							if rv.Type().AssignableTo(toField.Type()) {
								toField.Set(rv)
							}

							continue
						}

						if toField.CanSet() {
							if !set(toField, fromField) {
								if err := Copy(toField.Addr().Interface(), fromField.Interface()); err != nil {
									return err
								}
							}
						}
					} else {
						// try to set to method
						var toMethod reflect.Value
						if dest.CanAddr() {
							toMethod = dest.Addr().MethodByName(name)
						} else {
							toMethod = dest.MethodByName(name)
						}

						if toMethod.IsValid() && toMethod.Type().NumIn() == 1 && fromField.Type().AssignableTo(toMethod.Type().In(0)) {
							toMethod.Call([]reflect.Value{fromField})
						}
					}
				}
			}

			// Copy from method to field
			for _, field := range deepFields(toType) {
				name := field.Name

				var fromMethod reflect.Value
				if source.CanAddr() {
					fromMethod = source.Addr().MethodByName(name)
				} else {
					fromMethod = source.MethodByName(name)
				}

				if fromMethod.IsValid() && fromMethod.Type().NumIn() == 0 && fromMethod.Type().NumOut() == 1 {
					if toField := dest.FieldByName(name); toField.IsValid() && toField.CanSet() {
						values := fromMethod.Call([]reflect.Value{})
						if len(values) >= 1 {
							set(toField, values[0])
						}
					}
				}
			}
		}
		if isSlice {
			if dest.Addr().Type().AssignableTo(to.Type().Elem()) {
				to.Set(reflect.Append(to, dest.Addr()))
			} else if dest.Type().AssignableTo(to.Type().Elem()) {
				to.Set(reflect.Append(to, dest))
			}
		}
	}
	return
}

func isNullableType(t reflect.Type) bool {
	return t.ConvertibleTo(reflect.TypeOf((*driver.Valuer)(nil)).Elem())
}

func deepFields(reflectType reflect.Type) []reflect.StructField {
	var fields []reflect.StructField

	if reflectType = indirectType(reflectType); reflectType.Kind() == reflect.Struct {
		for i := 0; i < reflectType.NumField(); i++ {
			v := reflectType.Field(i)
			if v.Anonymous {
				fields = append(fields, deepFields(v.Type)...)
			} else {
				fields = append(fields, v)
			}
		}
	}

	return fields
}

func indirect(reflectValue reflect.Value) reflect.Value {
	for reflectValue.Kind() == reflect.Ptr {
		reflectValue = reflectValue.Elem()
	}
	return reflectValue
}

func indirectType(reflectType reflect.Type) reflect.Type {
	for reflectType.Kind() == reflect.Ptr || reflectType.Kind() == reflect.Slice {
		reflectType = reflectType.Elem()
	}
	return reflectType
}

func set(to, from reflect.Value) bool {
	for from.Kind() == reflect.Ptr {
		from = reflect.Indirect(from)
	}

	if from.IsValid() {
		for to.Kind() == reflect.Ptr {
			//set `to` to nil if from is nil
			if from.Kind() == reflect.Ptr && from.IsNil() {
				to.Set(reflect.Zero(to.Type()))
				return true
			} else if from.Kind() != reflect.String && from.IsNil() && to.IsNil() && to.Kind() == reflect.Ptr {
				pf := reflect.New(to.Type().Elem())
				if pf.Elem().Kind() == reflect.Ptr {
					to.Set(pf)
				}
				return true
			} else if to.IsNil() {
				// TODO: Commenting out because we don't need to set it.
				to.Set(reflect.New(to.Type().Elem()))
			}
			to = to.Elem()
		}

		if from.Type().ConvertibleTo(to.Type()) {
			to.Set(from.Convert(to.Type()))
		} else if scanner, ok := to.Addr().Interface().(sql.Scanner); ok {
			fromFieldInterface := from.Interface()
			if from.Kind() == reflect.Ptr {
				if !from.IsZero() {
					fromFieldInterface = from.Elem().Interface()
				} else {
					return true
				}
			}
			err := scanner.Scan(fromFieldInterface)
			if err != nil {
				return false
			}
		} else if from.Kind() == reflect.Ptr {
			return set(to, from.Elem())
		} else {
			return false
		}
	}
	return true
}
