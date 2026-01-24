package utils

// PtrToInt32 returns a pointer to the given int32 value
func PtrToInt32(i int) *int32 {
	v := int32(i)
	return &v
}

// PtrToInt64 returns a pointer to the given int64 value
func PtrToInt64(i int) *int64 {
	v := int64(i)
	return &v
}

// PtrToBool returns a pointer to the given bool value
func PtrToBool(b bool) *bool {
	return &b
}
