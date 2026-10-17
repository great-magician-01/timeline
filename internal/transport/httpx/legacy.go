package httpx

import (
	"errors"
	"reflect"
)

// legacyFailure 是迁移期兼容旧 internal/api.Failure 的最小视图。
//
// 目标架构不允许 transport 依赖旧 internal/api（旧包最终会删除），但迁移阶段仍有旧
// handler 把 *api.Failure 作为 error 交给新的 Respond。旧类型形状固定为
// Status/Code/Message/Details 四个导出字段，这里用反射识别字段形状，不 import 旧包。
//
// G8 收尾（删除旧 internal/api）时整个文件可直接删除。
type legacyFailure struct {
	status  int
	code    string
	message string
	details any
}

// legacyFailureOf 沿 error 链查找形状符合旧 api.Failure 的错误。
func legacyFailureOf(err error) (legacyFailure, bool) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		rv := reflect.ValueOf(e)
		if rv.Kind() != reflect.Pointer || rv.IsNil() {
			continue
		}
		rv = rv.Elem()
		if rv.Kind() != reflect.Struct {
			continue
		}
		statusField := rv.FieldByName("Status")
		codeField := rv.FieldByName("Code")
		messageField := rv.FieldByName("Message")
		if !statusField.IsValid() || !codeField.IsValid() || !messageField.IsValid() {
			continue
		}
		if statusField.Kind() != reflect.Int || codeField.Kind() != reflect.String || messageField.Kind() != reflect.String {
			continue
		}
		status := int(statusField.Int())
		code := codeField.String()
		if status < 400 || status > 599 || code == "" {
			continue
		}
		failure := legacyFailure{status: status, code: code, message: messageField.String()}
		if detailsField := rv.FieldByName("Details"); detailsField.IsValid() && detailsField.CanInterface() {
			failure.details = detailsField.Interface()
		}
		return failure, true
	}
	return legacyFailure{}, false
}
