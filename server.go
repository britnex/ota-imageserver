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
	"strings"
	"time"
)

var debug bool = false

var tarfolder string = "/tmp/"

func difftarhandler(w http.ResponseWriter, r *http.Request) {

	inputfname := tarfolder + r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

	if debug {
		fmt.Println("serving diff file " + inputfname)
	}

	gr, err := gzip.NewReader(r.Body)
	if err != nil {
		panic(err)
	}

	requestedfilesbitmap, err := ioutil.ReadAll(gr)
	defer r.Body.Close()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "500 - Cannot read request bitmap!")
		return
	}
	gr.Close()

	// step 1 : read tgz file and identify tar entries matching supplied hashes
	filein, err := os.Open(inputfname)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "404 - File not found!")
		return
	}
	archivein, err := gzip.NewReader(filein)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "500 - cannot read tgz file!")
		return
	}

	tr := tar.NewReader(archivein)

	r.Header.Set("Content-Type", "application/octet-stream")

	archiveout := gzip.NewWriter(w)
	tarout := tar.NewWriter(archiveout)

	var regularfileindex uint32 = 0
	for {

		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		if hdr.Typeflag == '0' && hdr.Size > 0 { // regular file

			var byteindex = regularfileindex / 8
			var bitindex = 7 - (regularfileindex % 8)

			regularfileindex++

			if byteindex > uint32(len(requestedfilesbitmap)) {
				fmt.Fprintf(tarout, ": fatal error")
				log.Fatalln("requestedfilesbitmap: out of bounds!")
				break
			}

			if (requestedfilesbitmap[byteindex]>>bitindex)&1 == 1 {
				// only include file if bit for this regularfileindex is set

				err = tarout.WriteHeader(hdr)
				if err != nil {
					panic(err)
				}
				if _, err := io.Copy(tarout, tr); err != nil {
					panic(err)
				}

				if debug {
					fmt.Printf("+ %s \n", hdr.Name)
				}
			}
		}

	}

	tarout.Close()
	archiveout.Close() // write gzip footer

	if debug {
		fmt.Printf("diff sent.\n")
	}
}

func indextarhandler(w http.ResponseWriter, r *http.Request) {

	inputfname := tarfolder + r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

	if debug {
		fmt.Println("serving index file " + inputfname)
	}

	filein, err := os.Open(inputfname)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "404 - File not found!")
		return
	}
	defer filein.Close()
	archivein, err := gzip.NewReader(filein)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "500 - cannot read tgz file!")
		return
	}
	defer archivein.Close()
	tr := tar.NewReader(archivein)

	r.Header.Set("Content-Type", "application/octet-stream")

	archiveout := gzip.NewWriter(w)
	tarout := tar.NewWriter(archiveout)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		if hdr.Typeflag == '0' && hdr.Size > 0 { // only regular files
			h := sha1.New()
			if _, err := io.Copy(h, tr); err != nil {
				log.Fatal(err)
			}
			hash := h.Sum(nil)

			hdr.Size = int64(sha1.Size)
			err = tarout.WriteHeader(hdr)
			if err != nil {
				panic(err)
			}
			_, err = tarout.Write(hash)
			if err != nil {
				panic(err)
			}

			if debug {
				hashstr := hex.EncodeToString(hash)
				fmt.Printf("%s : %s\n", hashstr, hdr.Name)
			}
		} else {
			tarout.WriteHeader(hdr)
			if hdr.Size > 0 {
				if _, err := io.Copy(tarout, tr); err != nil {
					panic(err)
				}
			}
		}

	}

	tarout.Close()
	archiveout.Close() // write gzip footer

	if debug {
		fmt.Printf("index sent.\n")
	}

}

func handler(w http.ResponseWriter, r *http.Request) {

	if r.Method == http.MethodGet {
		indextarhandler(w, r)
		return
	}
	if r.Method == http.MethodPost {
		difftarhandler(w, r)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
	fmt.Fprintf(w, "405 - unsupported method")

}

func main() {

	pbind := flag.String("bind", ":8090", "bin to this address and port")
	pdebug := flag.Bool("debug", false, "enable debug output")

	flag.Parse()

	if *pdebug {
		debug = true
	}

	http.HandleFunc("/", handler)

	server := &http.Server{
		Addr:         *pbind,
		ReadTimeout:  600 * time.Second,
		WriteTimeout: 600 * time.Second,
	}

	err := server.ListenAndServe()
	if err != nil {
		panic(err)
	}
	fmt.Println("done")
}
