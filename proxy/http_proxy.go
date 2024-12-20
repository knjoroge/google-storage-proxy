package http_cache

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"path/filepath"

	"cloud.google.com/go/storage"
)

type StorageProxy struct {
	bucketHandler *storage.BucketHandle
	defaultPrefix string
}

func NewStorageProxy(bucketHandler *storage.BucketHandle, defaultPrefix string) *StorageProxy {
	return &StorageProxy{
		bucketHandler: bucketHandler,
		defaultPrefix: defaultPrefix,
	}
}

func (proxy StorageProxy) objectName(name string) string {
	return proxy.defaultPrefix + name
}

func (proxy StorageProxy) Serve(address string, port int64) error {
	http.HandleFunc("/", proxy.handler)

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", address, port))

	if err == nil {
		address := listener.Addr().String()
		listener.Close()
		log.Printf("Starting http cache server %s\n", address)
		return http.ListenAndServe(address, nil)
	}
	return err
}

func (proxy StorageProxy) handler(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Path
	if key[0] == '/' {
		key = key[1:]
	}
	switch r.Method {
	case "GET":
		proxy.downloadBlob(w, key)
	case "HEAD":
		proxy.checkBlobExists(w, key)
	case "POST", "PUT":
		proxy.uploadBlob(w, r, key)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (proxy StorageProxy) downloadBlob(w http.ResponseWriter, name string) {
	object := proxy.bucketHandler.Object(proxy.objectName(name))
	if object == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	reader, err := object.NewReader(context.Background())
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer reader.Close()

	// Determine MIME type
	ext := filepath.Ext(name)
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)

	bufferedReader := bufio.NewReader(reader)
	_, err = bufferedReader.WriteTo(w)
	if err != nil {
		log.Printf("Failed to serve blob %q: %v", name, err)
	}
}

func (proxy StorageProxy) checkBlobExists(w http.ResponseWriter, name string) {
	object := proxy.bucketHandler.Object(proxy.objectName(name))
	if object == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// lookup attributes to see if the object exists
	attrs, err := object.Attrs(context.Background())
	if err != nil || attrs == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (proxy StorageProxy) uploadBlob(w http.ResponseWriter, r *http.Request, name string) {
	object := proxy.bucketHandler.Object(proxy.objectName(name))

	writer := object.NewWriter(context.Background())
	defer writer.Close()

	bufferedWriter := bufio.NewWriter(writer)
	bufferedReader := bufio.NewReader(r.Body)

	_, err := bufferedWriter.ReadFrom(bufferedReader)
	if err != nil {
		uploadBlobFailedResponse(w, err)
		return
	}

	if err := bufferedWriter.Flush(); err != nil {
		uploadBlobFailedResponse(w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func uploadBlobFailedResponse(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusBadRequest)
	errorMsg := fmt.Sprintf("Blob upload failed: %v", err)
	w.Write([]byte(errorMsg))
}
