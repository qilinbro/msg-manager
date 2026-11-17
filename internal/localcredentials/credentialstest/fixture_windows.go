//go:build windows

// Package credentialstest creates only synthetic, t.TempDir-scoped credentials
// for subprocess/integration tests. Production executables do not import it.
package credentialstest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const Purpose = "msg-manager/service-credential/v1"

func Write(t *testing.T, serviceURL, token string) string {
	t.Helper()
	plain, err := json.Marshal(map[string]any{"version": 1, "service_url": serviceURL, "api_token": token})
	if err != nil {
		t.Fatal(err)
	}
	return WritePlaintext(t, plain, Purpose)
}

// WritePlaintext encrypts synthetic bytes, including intentionally invalid
// records. The caller cannot choose an output outside this test's TempDir.
func WritePlaintext(t *testing.T, plain []byte, purpose string) string {
	t.Helper()
	if len(plain) == 0 || purpose == "" {
		t.Fatal("synthetic fixture needs non-empty plaintext and purpose")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	directory := filepath.Join(t.TempDir(), "private-synthetic-credentials")
	directorySD, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		t.Fatal(err)
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: directorySD}
	directoryName, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CreateDirectory(directoryName, &attributes); err != nil {
		t.Fatal(err)
	}
	entropy := []byte(purpose)
	input := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	optionalEntropy := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &optionalEntropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(plain)
	runtime.KeepAlive(entropy)
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	fileSD, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;" + sid + ")")
	if err != nil {
		t.Fatal(err)
	}
	attributes.SecurityDescriptor = fileSD
	path := filepath.Join(directory, "synthetic-service.dpapi")
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, &attributes, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(handle), "synthetic protected credential")
	if _, err := file.Write(unsafe.Slice(output.Data, int(output.Size))); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
