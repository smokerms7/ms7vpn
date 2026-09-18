// Утилита сборки: складывает payload.zip для установщика.
//
// Раньше архив собирался вручную, и именно поэтому в него однажды не попали
// geo-файлы, а установщик WebView2 попал, но не запускался.
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	output := flag.String("o", "cmd/installer/payload.zip", "куда положить архив")
	flag.Parse()

	sources := flag.Args()
	if len(sources) == 0 {
		fmt.Fprintln(os.Stderr, "использование: mkpayload -o payload.zip ФАЙЛ [ФАЙЛ...]")
		os.Exit(2)
	}

	if err := build(*output, sources); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func build(output string, sources []string) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(file)

	var total int64
	for _, source := range sources {
		info, err := os.Stat(source)
		if err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		if info.IsDir() {
			return fmt.Errorf("%s: это папка, нужен файл", source)
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.Base(source)
		header.Method = zip.Deflate

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(writer, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		total += written
		fmt.Printf("  + %-34s %8.2f МБ\n", header.Name, float64(written)/(1<<20))
	}

	if err := archive.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	packed, err := os.Stat(output)
	if err != nil {
		return err
	}
	fmt.Printf("  = %-34s %8.2f МБ (из %.2f МБ)\n",
		strings.TrimPrefix(output, "./"), float64(packed.Size())/(1<<20), float64(total)/(1<<20))
	return nil
}
