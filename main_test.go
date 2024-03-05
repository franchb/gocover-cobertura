package main_test

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"fortio.org/assert"
	cobertura "github.com/franchb/gocover-cobertura"
	"golang.org/x/tools/go/packages"
)

func Test_Main(t *testing.T) {
	t.Parallel()
	fname := filepath.Join(t.TempDir(), "stdout")
	temp, err := os.Create(fname)
	assert.NoError(t, err)
	os.Stdout = temp
	assert.NoError(t, cobertura.Run())
	outputBytes, err := os.ReadFile(fname)
	assert.NoError(t, err)

	outputString := string(outputBytes)
	assert.Contains(t, outputString, xml.Header)
	assert.Contains(t, outputString, cobertura.DTDDecl)
}

func TestConvertParseProfilesError(t *testing.T) {
	t.Parallel()
	pipe2rd, pipe2wr := io.Pipe()
	t.Cleanup(func() {
		err := pipe2rd.Close()
		assert.NoError(t, err)
		err = pipe2wr.Close()
		assert.NoError(t, err)
	})
	err := cobertura.Convert(strings.NewReader("invalid data"), pipe2wr, &cobertura.Ignore{})
	assert.Error(t, err)
	assert.Equal(t, "bad mode line: invalid data", err.Error())
}

func TestConvertOutputError(t *testing.T) {
	t.Parallel()
	pipe2rd, pipe2wr := io.Pipe()
	err := pipe2wr.Close()
	assert.NoError(t, err)
	t.Cleanup(func() { err := pipe2rd.Close(); assert.NoError(t, err) })
	err = cobertura.Convert(strings.NewReader("mode: set"), pipe2wr, &cobertura.Ignore{})
	assert.Error(t, err)
	assert.Equal(t, "io: read/write on closed pipe", err.Error())
}

func TestConvertEmpty(t *testing.T) {
	t.Parallel()
	data := `mode: set`

	pipe2rd, pipe2wr := io.Pipe()
	go func() {
		err := cobertura.Convert(strings.NewReader(data), pipe2wr, &cobertura.Ignore{})
		assert.NoError(t, err)
	}()

	value := cobertura.Coverage{}
	dec := xml.NewDecoder(pipe2rd)
	err := dec.Decode(&value)
	assert.NoError(t, err)

	assert.Equal(t, "coverage", value.XMLName.Local)
	assert.True(t, value.Sources == nil)
	assert.True(t, value.Packages == nil)
}

func TestParseProfileNilPackages(t *testing.T) {
	t.Parallel()
	v := cobertura.Coverage{}
	profile := cobertura.Profile{FileName: "does-not-exist"}
	err := v.ParseProfile(&profile, nil, &cobertura.Ignore{})
	assert.Error(t, err)
	assert.Contains(t, `package required when using go modules`, err.Error())
}

func TestParseProfileEmptyPackages(t *testing.T) {
	t.Parallel()
	v := cobertura.Coverage{}
	profile := cobertura.Profile{FileName: "does-not-exist"}
	err := v.ParseProfile(&profile, &packages.Package{}, &cobertura.Ignore{})
	assert.Error(t, err)
	assert.Contains(t, `package required when using go modules`, err.Error())
}

func TestParseProfileDoesNotExist(t *testing.T) {
	t.Parallel()
	value := cobertura.Coverage{}
	profile := cobertura.Profile{FileName: "does-not-exist"}

	pkg := packages.Package{
		Name:   "does-not-exist",
		Module: &packages.Module{},
	}

	err := value.ParseProfile(&profile, &pkg, &cobertura.Ignore{})
	assert.Error(t, err)

	// Windows vs. Linux
	if !strings.Contains(err.Error(), "system cannot find the file specified") &&
		!strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf(err.Error())
	}
}

func TestParseProfileNotReadable(t *testing.T) {
	t.Parallel()
	v := cobertura.Coverage{}
	profile := cobertura.Profile{FileName: os.DevNull}
	err := v.ParseProfile(&profile, nil, &cobertura.Ignore{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "package required when using go modules")
}

func TestParseProfilePermissionDenied(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("chmod is not supported by Windows")
	}

	tempFile, err := os.CreateTemp(t.TempDir(), "not-readable")
	assert.NoError(t, err)

	t.Cleanup(func() { err := os.Remove(tempFile.Name()); assert.NoError(t, err) })

	err = tempFile.Chmod(0o00)
	assert.NoError(t, err)
	value := cobertura.Coverage{}
	profile := cobertura.Profile{FileName: tempFile.Name()}
	pkg := packages.Package{
		GoFiles: []string{
			tempFile.Name(),
		},
		Module: &packages.Module{
			Path: filepath.Dir(tempFile.Name()),
		},
	}
	err = value.ParseProfile(&profile, &pkg, &cobertura.Ignore{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

//nolint:cyclop // long test
func TestConvertSetMode(t *testing.T) {
	t.Parallel()
	pipe1rd, err := os.Open("testdata/testdata_set.txt")
	assert.NoError(t, err)

	pipe2rd, pipe2wr := io.Pipe()

	var convwr io.Writer = pipe2wr

	if os.Getenv("SAVE_TEST_OUTPUT") != "true" {
		testwr, err := os.Create("testdata/testdata_set.xml")
		if err != nil {
			t.Fatal("Can't open output testdata.", err)
		}

		t.Cleanup(func() { assert.NoError(t, testwr.Close()) })

		convwr = io.MultiWriter(convwr, testwr)
	}

	go func() {
		if convertErr := cobertura.Convert(pipe1rd, convwr, &cobertura.Ignore{
			GeneratedFiles: true,
			Files:          regexp.MustCompile(`[\\/]func[45]\.go$`),
		}, "testdata"); convertErr != nil {
			panic(convertErr)
		}
	}()

	value := cobertura.Coverage{}
	dec := xml.NewDecoder(pipe2rd)
	err = dec.Decode(&value)
	assert.NoError(t, err)

	assert.Equal(t, "coverage", value.XMLName.Local)
	assert.Equal(t, len(value.Sources), 1)
	assert.Equal(t, len(value.Packages), 1)

	pkg := value.Packages[0]
	assert.Equal(t, "github.com/franchb/gocover-cobertura/testdata", strings.TrimRight(pkg.Name, "/"))
	assert.True(t, pkg.Classes != nil)
	assert.Equal(t, len(pkg.Classes), 2)

	class := pkg.Classes[0]
	assert.Equal(t, "-", class.Name)
	assert.Equal(t, "testdata/func1.go", class.Filename)
	assert.True(t, class.Methods != nil)
	assert.Equal(t, len(class.Methods), 1)
	assert.True(t, class.Lines != nil)
	assert.Equal(t, len(class.Lines), 4)

	method := class.Methods[0]
	assert.Equal(t, "Func1", method.Name)
	assert.True(t, method.Lines != nil)
	assert.Equal(t, len(method.Lines), 4)

	var line *cobertura.Line
	if line = method.Lines[0]; line.Number != 5 || line.Hits != 1 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", line.Number, line.Hits)
	}
	if line = method.Lines[1]; line.Number != 6 || line.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", line.Number, line.Hits)
	}
	if line = method.Lines[2]; line.Number != 7 || line.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", line.Number, line.Hits)
	}
	if line = method.Lines[3]; line.Number != 8 || line.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", line.Number, line.Hits)
	}

	if line = class.Lines[0]; line.Number != 5 || line.Hits != 1 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", line.Number, line.Hits)
	}
	if line = class.Lines[1]; line.Number != 6 || line.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", line.Number, line.Hits)
	}
	if line = class.Lines[2]; line.Number != 7 || line.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", line.Number, line.Hits)
	}
	if line = class.Lines[3]; line.Number != 8 || line.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", line.Number, line.Hits)
	}

	class = pkg.Classes[1]
	assert.Equal(t, "Type1", class.Name)
	assert.Equal(t, "testdata/func2.go", class.Filename)
	assert.True(t, class.Methods != nil)
	assert.Equal(t, len(class.Methods), 3)
}
