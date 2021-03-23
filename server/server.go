package server

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/joho/godotenv"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
)

var MapboxAccessToken string

func init() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("error loading .env file")
	}
	MapboxAccessToken = os.Getenv("MAPBOX_ACCESS_TOKEN")
}

func Run() {

	r := chi.NewRouter()
	s := &http.Server{
		Addr:         ":8080",
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	r.Use(middleware.Logger)
	r.Get("/data/{z}/{x}/{y}", handleData)

	fileServer(r)

	panic(s.ListenAndServe())
}

func handleData(w http.ResponseWriter, r *http.Request) {

	// parse params from incoming request
	x := chi.URLParam(r, "x")
	y := chi.URLParam(r, "y")
	z := chi.URLParam(r, "z")

	// writeEmptyLayer(w)
	writeTileLayers(w, x, y, z)
	return
}

func fileServer(router *chi.Mux) {
	root := "./web"
	fs := http.FileServer(http.Dir(root))

	router.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(root + r.RequestURI); os.IsNotExist(err) {
			http.StripPrefix(r.RequestURI, fs).ServeHTTP(w, r)
		} else {
			fs.ServeHTTP(w, r)
		}
	})
}

func errServer(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), 500)
}

func getExternalMVT(x, y, z uint32) (mvt.Layers, error) {
	// create new url to request an mvt tile
	// w/ same z/x/y slippy tile as incoming request
	u := url.URL{
		Scheme: "https",
		Host:   "api.mapbox.com",
		Path:   fmt.Sprintf("v4/mapbox.mapbox-streets-v7/%d/%d/%d.vector.pbf", z, x, y),
	}

	q := u.Query()
	q.Set("access_token", MapboxAccessToken)
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// decode mvt layers using paulmach/orb/encoding/mvt
	layers, err := mvt.Unmarshal(b)
	if err != nil {
		return nil, err
	}

	return layers, nil
}

func printLayers(layers mvt.Layers) {
	for _, layer := range layers {
		fmt.Printf("%s: %d features\n", layer.Name, len(layer.Features))
	}
}

func writeEmptyLayer(w http.ResponseWriter) {
	// send back empty mvt layer just to keep mapbox-gl-js happy
	emptyLayers := mvt.Layers{
		&mvt.Layer{Name: "empty"},
	}
	emptyB, _ := mvt.Marshal(emptyLayers)
	w.Write(emptyB)
}

func writeTileLayers(w http.ResponseWriter, x, y, z string) {

	xi, err := strconv.Atoi(x)
	if err != nil {
		errServer(w, err)
		return
	}

	yi, err := strconv.Atoi(y)
	if err != nil {
		errServer(w, err)
		return
	}

	zi, err := strconv.Atoi(z)
	if err != nil {
		errServer(w, err)
		return
	}

	tile := maptile.New(uint32(xi), uint32(yi), maptile.Zoom(zi))

	tiles := tile.Children()
	// tiles := maptile.Tiles{tile}

	// add tile borders
	fcTileBorders := tiles.ToFeatureCollection()
	layerTileBorders := mvt.NewLayer("tile", fcTileBorders)

	// add tile center text
	fcTileText := geojson.NewFeatureCollection()
	for _, t := range tiles {

		text := fmt.Sprintf(`tile: %d/%d/%d`, t.Z, t.X, t.Y)

		// todo: maybe parallize this in goroutines
		layers, err := getExternalMVT(t.X, t.Y, uint32(t.Z))
		if err != nil {
			log.Println(err)
		}

		text += "\nlayer: # features"
		for _, l := range layers {
			text += fmt.Sprintf("\n%s: %d", l.Name, len(l.Features))
		}

		c := t.Center()
		f := geojson.NewFeature(c)
		f.Properties["text"] = text
		fcTileText.Append(f)
	}
	layerTileText := mvt.NewLayer("text", fcTileText)

	// create layers
	layers := mvt.Layers{
		layerTileBorders,
		layerTileText,
	}

	// default orb/encoding/mvt sets layer to version 1
	// but mapboxgl doesnt like this so we set to version 2
	for i := range layers {
		layers[i].Version = uint32(2)
	}

	layers.ProjectToTile(tile)

	b, err := mvt.Marshal(layers)
	if err != nil {
		errServer(w, err)
		return
	}

	log.Printf("%s/%s/%s\n", z, x, y)
	w.Write(b)
}
