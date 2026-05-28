package models

// PermissionDescriptor mirrors the auth service register payload.
type PermissionDescriptor struct {
	Name        string
	Description string
}

// PermissionDescriptors returns PM workspace permissions registered at startup.
func PermissionDescriptors() []PermissionDescriptor {
	return []PermissionDescriptor{
		{Name: "pm.view_workspace", Description: "View project management workspace"},
		{Name: "pm.mutate_workspace", Description: "Create and update workspace entities"},
		{Name: "pm.admin", Description: "Manage workspace members and org metadata"},
	}
}
