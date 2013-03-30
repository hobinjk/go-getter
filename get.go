package main

import "net/http"
import "fmt"
import "flag"
import "io"
import "archive/zip"
import "archive/tar"
import "compress/gzip"
import "os"
import "strings"
import "compress/bzip2"

type HttpReaderAt struct {
	URL           string
	RespData      chan byte
	Data          []byte
	ContentLength int64
	ReadIndex     int64
	Verbose       bool
}

func (r *HttpReaderAt) Read(p []byte) (n int, err error) {
	n, err = r.ReadAt(p, r.ReadIndex)
	r.ReadIndex += int64(n)
	return
}

func (r *HttpReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	n = len(p)
	if cap(r.Data) <= len(p)+int(off) {
		t := make([]byte, len(r.Data), len(p)+int(off)+8192)
		copy(t, r.Data)
		r.Data = t
	}
	if len(r.Data) <= len(p)+int(off) {
		prevLen := len(r.Data)
		r.Data = r.Data[0 : len(p)+int(off)]
		for i := prevLen; i < int(off)+len(p); i++ {
			var good bool
			r.Data[i], good = <-r.RespData
			if !good {
				n = i - prevLen //please never be negative please oh god please
				err = io.EOF   //slightly arbitrary
				break
			}
		}
	}
	copy(p, r.Data[int(off):int(off)+len(p)])
	return n, err
}

func (r *HttpReaderAt) Start() {
	trans := &http.Transport{
		DisableCompression: true,
	}
	client := &http.Client{Transport: trans}

	r.RespData = make(chan byte, 8192)
	resp, err := client.Get(r.URL)
	if err != nil {
		close(r.RespData)
		return
	}
	r.ContentLength = resp.ContentLength
	r.ReadIndex = 0
	if resp.ContentLength < 0 { //unlimited
		r.Data = make([]byte, 0, 8192)
	} else {
		r.Data = make([]byte, 0, resp.ContentLength)
	}
	go func() {
		buf := make([]byte, 16*1024)

		for {
			n, err := resp.Body.Read(buf)
			for i := 0; i < n; i++ {
				r.RespData <- buf[i]
			}
			if err != nil {
				resp.Body.Close()
				close(r.RespData)
				return
			}
		}
	}()
}

func CreateParents(name string) error {
	var base string = ""
	dirs := strings.Split(name, string(os.PathSeparator))
	dirs = dirs[0 : len(dirs)-1]
	if len(dirs) == 0 {
		return nil
	}
	for _, dirname := range dirs {
		base += dirname + string(os.PathSeparator)
		err := os.Mkdir(base, 0777)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func WriteAllAndClose(reader io.ReadCloser, f *os.File) error {
	err := WriteAll(reader, f)
	reader.Close()
	return err
}
func WriteAll(reader io.Reader, f *os.File) error {
	_, err := io.Copy(f, reader)
	if err != nil {
		fmt.Println(err)
		return err
	}
	f.Close()
	return nil
}

func (hra *HttpReaderAt) ReadZip() error {
	reader, err := zip.NewReader(hra, hra.ContentLength)
	if err != nil {
		return err
	}
	for _, zf := range reader.File {
		if hra.Verbose {
      fmt.Printf("%s\n", zf.Name)
    }
		err = CreateParents(zf.Name)
		if err != nil {
			fmt.Println(err)
			continue
		}
		f, err := os.Create(zf.Name)
		if err != nil {
			fmt.Println(err)
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			fmt.Println(err)
			continue
		}
		go WriteAllAndClose(rc, f)
	}
	return nil
}

func (hra *HttpReaderAt) ReadTarGz() error {
	reader, err := gzip.NewReader(hra)
	if err != nil {
		return err
	}
	return hra.ReadTar(reader)
}

func (hra *HttpReaderAt) ReadTarBz2() error {
	reader := bzip2.NewReader(hra)
	return hra.ReadTar(reader)
}

func (hra *HttpReaderAt) ReadTar(reader io.Reader) error {
	tre := tar.NewReader(reader)
	for {
		header, err := tre.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hra.Verbose {
			fmt.Println(header.Name)
		}
		if strings.HasSuffix(header.Name, "/") {
			continue
		}

		err = CreateParents(header.Name)
		if err != nil {
			fmt.Println(err)
		}
		f, err := os.Create(header.Name)
		if err != nil {
			continue
		}
    err = WriteAll(tre, f)
    if err != nil {
      fmt.Println(err)
    }
	}
	return nil
}

func Usage() {
	fmt.Fprintf(os.Stderr, "get: gets stuff\nUsage: %s [-v] url\n", os.Args[0])
	flag.PrintDefaults()
}
func main() {
	flag.Usage = Usage
	var verbose *bool = flag.Bool("v", false, "verbosity")
	flag.Parse()
	url := flag.Arg(0)
	if len(url) == 0 {
		flag.Usage()
		return
	}

	hra := &HttpReaderAt{URL: url}
	hra.Start()
	hra.Verbose = *verbose
	if strings.HasSuffix(url, ".zip") {
		err := hra.ReadZip()
		if err != nil {
			fmt.Println(err)
		}
	} else if strings.HasSuffix(url, ".tar.gz") {
		err := hra.ReadTarGz()
		if err != nil {
			fmt.Println(err)
		}
	} else if strings.HasSuffix(url, ".tar.bz2") {
		err := hra.ReadTarBz2()
		if err != nil {
			fmt.Println(err)
		}
	} else if strings.HasSuffix(url, ".tar") {
		err := hra.ReadTar(hra)
		if err != nil {
			fmt.Println(err)
		}
	} else {
		parts := strings.Split(url, "/")
		name := parts[len(parts)-1]
		f, err := os.Create(name)
		if err != nil {
			fmt.Println(err)
			return
		}
    if hra.ContentLength > 0 {
      buf := make([]byte, hra.ContentLength)
      n, err := hra.ReadAt(buf, 0)
      if err != nil {
        fmt.Println(err)
        return
      }
      f.Write(buf[0:n])
		  f.Close()
    } else {
      buf := make([]byte, 16*1024)
      for {
        n, err := hra.Read(buf)
        if n > 0 {
          f.Write(buf[0:n])
        }
        if err == io.EOF {
          break
        }
        if err != nil {
          fmt.Println(err)
          return
        }
      }
      f.Close()
    }
	}
}
