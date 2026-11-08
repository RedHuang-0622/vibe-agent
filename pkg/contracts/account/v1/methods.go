package accountv1

const (
	MethodReadGet         = "account.read.get"
	MethodReadResolve     = "account.read.resolve"
	MethodAdminList       = "account.admin.list"
	MethodAdminCreate     = "account.admin.create"
	MethodAdminUpdate     = "account.admin.update"
	MethodAdminSetDefault = "account.admin.set_default"
	MethodAdminSetEnabled = "account.admin.set_enabled"
	MethodAdminRotateRef  = "account.admin.rotate_credential_ref"
	MethodAdminArchive    = "account.admin.archive"
	MethodAdminValidate   = "account.admin.validate"
	MethodAdminSmoke      = "account.admin.smoke"
)

func AllMethods() []string {
	return []string{
		MethodReadGet,
		MethodReadResolve,
		MethodAdminList,
		MethodAdminCreate,
		MethodAdminUpdate,
		MethodAdminSetDefault,
		MethodAdminSetEnabled,
		MethodAdminRotateRef,
		MethodAdminArchive,
		MethodAdminValidate,
		MethodAdminSmoke,
	}
}

func IsAccountMethod(method string) bool {
	switch method {
	case MethodReadGet, MethodReadResolve,
		MethodAdminList, MethodAdminCreate, MethodAdminUpdate,
		MethodAdminSetDefault, MethodAdminSetEnabled, MethodAdminRotateRef,
		MethodAdminArchive, MethodAdminValidate, MethodAdminSmoke:
		return true
	default:
		return false
	}
}

func IsSessiondReadMethod(method string) bool {
	switch method {
	case MethodReadGet, MethodReadResolve:
		return true
	default:
		return false
	}
}

func IsWriteMethod(method string) bool {
	switch method {
	case MethodAdminCreate, MethodAdminUpdate, MethodAdminSetDefault,
		MethodAdminSetEnabled, MethodAdminRotateRef, MethodAdminArchive:
		return true
	default:
		return false
	}
}
