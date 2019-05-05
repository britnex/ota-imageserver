/*
 * This file is part of the ota-imageserver distribution (https://github.com/britnex/ota-imageserver).
 * Copyright (c) 2019 Andre Massow britnex@gmail.com
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, version 3.
 *
 * This program is distributed in the hope that it will be useful, but
 * WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
 * General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
)

type FileHeader struct {
	Hash [sha1.Size]byte
	Size uint32
}

var debug bool = false

func copyfile(src string, dst string) error {

	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()
	_, err = io.Copy(destination, source)
	if err != nil {
		return err
	}

	return nil
}

func getfilehash(src string) (string, error) {

	filein, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer filein.Close()

	h := sha1.New()
	if _, err := io.Copy(h, filein); err != nil {
		return "", err
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum), nil

}

func main() {

	defaulturl := "http://localhost:8090/image-1234.tgz"

	ptgzsrc := flag.String("src", defaulturl, "image download url (required argument)")
	ptgzdst := flag.String("dst", "./", "Save archive to <dst> directory")
	ptgzref := flag.String("ref", "/", "Reference directory")
	pdebug := flag.Bool("debug", false, "enable debug output")

	flag.Parse()

	if *ptgzsrc == defaulturl {
		fmt.Println("usage:")
		flag.PrintDefaults()
		os.Exit(1)
	}
	if *pdebug {
		debug = true
	}

	tgzsrc := *ptgzsrc
	tgzdst := *ptgzdst
	tgzref := *ptgzref

	if strings.HasSuffix(tgzsrc, ".tgz") == false {
		log.Fatalln("<src> argument requires .tgz suffix")
		os.Exit(2)
	}

	if strings.HasSuffix(tgzref, "/") == false {
		// ensure "/" suffix
		tgzref = tgzref + "/"
	}

	if strings.HasSuffix(tgzdst, "/") {
		// <dst> is directory
		tgzdst = tgzdst + path.Base(tgzsrc)
	} else if strings.HasSuffix(tgzdst, ".tgz") {
		// <dst> is .tgz filename
	} else {
		// ensure "/" suffix
		tgzdst = tgzdst + "/"
		tgzdst = tgzdst + path.Base(tgzsrc)
	}

	if debug {

		fmt.Printf("src: %s\n", tgzsrc)
		fmt.Printf("dst: %s\n", tgzdst)
		fmt.Printf("ref: %s\n", tgzref)

	}

	// step 1 : load "index" from server

	fmt.Printf("downloading index from %s to %s\n", tgzsrc, tgzdst)

	resp, err := http.Get(tgzsrc)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	// save index file to tmp filename
	tmpindexfile, err := ioutil.TempFile("/tmp/", "index-")
	if err != nil {
		panic(err)
	}
	if _, err := io.Copy(tmpindexfile, resp.Body); err != nil {
		panic(err)
	}
	tmpindexfile.Close()
	defer os.Remove(tmpindexfile.Name())

	tmpindexin, err := os.Open(tmpindexfile.Name())
	if err != nil {
		panic(err)
	}
	defer tmpindexin.Close()

	archivein, err := gzip.NewReader(tmpindexin)
	if err != nil {
		panic(err)
	}
	tr := tar.NewReader(archivein)

	fileout, err := os.Create(tgzdst)
	if err != nil {
		panic(err)
	}
	defer fileout.Close()
	archiveout := gzip.NewWriter(fileout)
	trout := tar.NewWriter(archiveout)

	var requestefilesbitmap bytes.Buffer

	var hash = make([]byte, sha1.Size)

	var regularfileindex uint32 = 0
	var bitmapbyte byte = 0

	var missingfiles uint32 = 0

	for {

		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {

			log.Fatal(err)
		}

		if hdr.Typeflag == '0' && hdr.Size > 0 {

			var bitindex = 7 - (regularfileindex % 8)

			regularfileindex++

			var hashstr string
			{ // parse hash
				n, err := tr.Read(hash)
				if (err != nil && err != io.EOF) || (n != sha1.Size) {
					log.Fatalln("Server responded with an unknown file hash format!")
					os.Exit(3)
				}
				hashstr = hex.EncodeToString(hash)
			}

			tmpfilename := "/tmp/" + hashstr + ".tmp"

			var uselocalfile bool = true
			{ // copy file to tmp
				err = copyfile(tgzref+hdr.Name, tmpfilename)
				if err != nil {
					// cannot copy file => request from server

					if debug {
						fmt.Printf("file does not (yet) exists: %s\n", hdr.Name)
					}

					uselocalfile = false
				}
			}

			if uselocalfile { // get file size
				fi, err := os.Stat(tmpfilename)
				if err != nil {

					if debug {
						fmt.Printf("file exists, cannot get file size : %s\n", hdr.Name)
					}

					uselocalfile = false
				}
				// change size to actual size of file
				hdr.Size = fi.Size()
			}

			if uselocalfile { // compare file hashes
				filehashstr, err := getfilehash(tmpfilename)
				if err != nil || filehashstr != hashstr {

					if debug {
						fmt.Printf("file exists, hash does not match: %s\n", hdr.Name)
					}

					uselocalfile = false
				}
			}

			if uselocalfile == false {
				// set bit to 1 = request this file
				bitmapbyte = bitmapbyte | (1 << bitindex)
			}
			if bitindex == 0 {
				requestefilesbitmap.WriteByte(bitmapbyte)
				bitmapbyte = 0 // set all bits to 0
			}

			if uselocalfile == false {
				os.Remove(tmpfilename)
				// request file from server
				missingfiles++
				continue
			}

			// write header of this file
			trout.WriteHeader(hdr)

			{ // write tmp file to output archive
				fi, err := os.Open(tmpfilename)
				if err != nil {
					panic("cannot read local file. should never happen, because getfilehash was successful before!")
				}

				if _, err := io.Copy(trout, fi); err != nil {

					panic(err)
				}
				fi.Close()
				os.Remove(tmpfilename)
			}

			if debug {
				fmt.Printf("> %s\n", hdr.Name)

			}
		} else {
			// include dirs, links .. without changes
			trout.WriteHeader(hdr)
			if hdr.Size > 0 {
				if _, err := io.Copy(trout, tr); err != nil {

					log.Fatal(err)
				}
			}
		}

	}

	// always include current bitmapbyte (even if empty)
	requestefilesbitmap.WriteByte(bitmapbyte)

	// step 2 : "load missing files" from server

	fmt.Printf("downloading %d missing files from %s\n", missingfiles, tgzsrc)

	if missingfiles > 0 {

		var w bytes.Buffer
		gw, err := gzip.NewWriterLevel(&w, gzip.BestCompression)
		if err != nil {
			panic(err)
		}
		gw.Write(requestefilesbitmap.Bytes())
		gw.Close()

		respp, err := http.Post(tgzsrc, "application/octet-stream", &w)
		if err != nil {
			panic(err)
		}

		defer respp.Body.Close()

		// save diff file to tmp filename
		tmpdifffile, err := ioutil.TempFile("/tmp/", "diff-")
		if err != nil {
			panic(err)
		}

		if _, err := io.Copy(tmpdifffile, respp.Body); err != nil {
			panic(err)
		}
		tmpdifffile.Close()

		tmpdiffin, err := os.Open(tmpdifffile.Name())
		if err != nil {
			panic(err)
		}
		defer tmpdiffin.Close()

		archivein, err = gzip.NewReader(tmpdiffin)
		if err != nil {
			panic(err)
		}
		tr = tar.NewReader(archivein)

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Fatal(err)
			}

			if debug {
				fmt.Printf("< %s \n", hdr.Name)
			}

			// included downloaded files into archive
			trout.WriteHeader(hdr)
			if hdr.Size > 0 {
				if _, err := io.Copy(trout, tr); err != nil {

					log.Fatal(err)
				}
			}

		}

		os.Remove(tmpdifffile.Name())
	}

	trout.Close()
	archiveout.Close() // write gzip footer

	fmt.Println("done")
}
