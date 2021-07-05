package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"

	_ "golang.org/x/image/bmp"

	"github.com/BurntSushi/graphics-go/graphics"
	"github.com/kolesa-team/go-webp/encoder"
	"github.com/kolesa-team/go-webp/webp"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"gopkg.in/yaml.v2"
)

type MinIO struct {
	EndPoint  string `yaml:"end_point"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	SSL       bool   `yaml:"ssl"`
}

type Config struct {
	Listen    string  `yaml:"listen"`
	Quality   float32 `yaml:"quality"`
	X         int     `yaml:"x"`
	Y         int     `yaml:"y"`
	ThumbPath string  `yaml:"thumb_path"`
	Bucket    string  `yaml:"bucket"`
	MinIO     MinIO   `yaml:"minio"`
}

func (m *MinIO) Client() (*minio.Client, error) {
	return minio.New(m.EndPoint, &minio.Options{
		Creds:  credentials.NewStaticV4(m.AccessKey, m.SecretKey, ""),
		Secure: m.SSL,
	})
}

var cfgFile string

func main() {
	flag.StringVar(&cfgFile, "config", "/etc/gothumb/config.yaml", "config file path")
	flag.Parse()

	if _, err := os.Stat(cfgFile); os.IsNotExist(err) {
		out, err := yaml.Marshal(&Config{MinIO: MinIO{}})
		if err != nil {
			log.Fatalln(err)
			return
		}
		err = ioutil.WriteFile(cfgFile, out, 0644)
		if err != nil {
			log.Fatalln(err)
			return
		}
		return
	}

	// read and load config files
	file, err := os.ReadFile(cfgFile)
	if err != nil {
		log.Fatalln(err)
		return
	}
	config := &Config{}
	err = yaml.Unmarshal(file, config)
	if err != nil {
		log.Fatalln(err)
		return
	}

	// prepare minio client
	client, err := config.MinIO.Client()
	if err != nil {
		log.Fatalln(err)
		return
	}
	// check bucket
	exists, err := client.BucketExists(context.Background(), config.Bucket)
	if err != nil {
		log.Fatalln(err)
		return
	}
	if !exists {
		log.Fatalf("bucket \"%s\" not found.", config.Bucket)
		return
	}

	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		thumbPath := filepath.Join(config.ThumbPath, path)

		exist := true
		_, err := client.StatObject(context.Background(), config.Bucket, thumbPath, minio.StatObjectOptions{})
		if err != nil {
			switch err.(type) {
			case minio.ErrorResponse:
				if err.(minio.ErrorResponse).Code == "NoSuchKey" {
					exist = false
					break
				} else {
					log.Println(err)
					return
				}
			default:
				log.Println(err)
				return
			}
		}

		if exist {
			thumb, err := client.GetObject(context.Background(), config.Bucket, thumbPath, minio.GetObjectOptions{})
			if err != nil {
				log.Println(err)
				return
			}
			_, err = io.Copy(w, thumb)
			if err != nil {
				log.Println(err)
				return
			}
		} else {
			object, err := client.GetObject(context.Background(), config.Bucket, path, minio.GetObjectOptions{})
			if err != nil {
				log.Println(err)
				return
			}
			defer object.Close()
			srcImage, _, err := image.Decode(object)
			if err != nil {
				log.Println(err)
				return
			}
			dstImage := image.NewRGBA(image.Rect(0, 0, config.X, config.Y))
			err = graphics.Thumbnail(dstImage, srcImage)
			if err != nil {
				log.Println(err)
				return
			}

			options, err := encoder.NewLossyEncoderOptions(encoder.PresetDefault, config.Quality)
			if err != nil {
				log.Println(err)
				return
			}

			buf := &bytes.Buffer{}

			err = webp.Encode(buf, dstImage, options)
			if err != nil {
				log.Println(err)
				return
			}

			go w.Write(buf.Bytes())

			_, err = client.PutObject(context.Background(), config.Bucket, thumbPath,
				bytes.NewReader(buf.Bytes()), int64(buf.Len()), minio.PutObjectOptions{
					ContentType: "image/webp",
				})
			if err != nil {
				log.Println(err)
				return
			}
		}
	})

	fmt.Println("Listening at:", config.Listen)
	log.Fatalln(http.ListenAndServe(config.Listen, nil))
}
