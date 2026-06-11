package media

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
)

func TestHLSServer(t *testing.T) {
	const testFile = "/tmp/test_garage.mp4"
	if _, err := os.Stat(testFile); err != nil {
		t.Skip("no test file")
	}
	if os.Getenv("HLS_SERVE") == "" {
		t.Skip("set HLS_SERVE=1")
	}

	// Extract init segment
	f, _ := os.Open(testFile)
	initBuf := make([]byte, 680) // ftyp(32) + moov(648)
	f.Read(initBuf)
	f.Close()
	os.WriteFile("/tmp/init_garage.mp4", initBuf, 0644)

	result, err := GenerateHLSPlaylist([]string{testFile}, []string{"/seg"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	http.HandleFunc("/test.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, result.Playlist)
	})
	http.HandleFunc("/seg/hls/init.mp4", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Init request")
		http.ServeFile(w, r, "/tmp/init_garage.mp4")
	})
	http.HandleFunc("/seg/hls/", func(w http.ResponseWriter, r *http.Request) {
		var segNum int
		fmt.Sscanf(r.URL.Path, "/seg/hls/%d", &segNum)
		if segNum < 0 || segNum >= len(result.Segments) {
			http.Error(w, "not found", 404)
			return
		}
		ref := result.Segments[segNum]
		w.Header().Set("Content-Type", "video/mp4")
		if err := ServeHLSSegment(w, ref.FilePath, ref.ByteStart, ref.ByteEnd); err != nil {
			log.Printf("Seg %d error: %v", segNum, err)
		} else {
			log.Printf("Seg %d OK", segNum)
		}
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><head>
<script src="https://cdn.jsdelivr.net/npm/hls.js@1"></script>
</head><body>
<video id="v" controls muted autoplay playsinline style="width:100%;max-width:800px"></video>
<pre id="log" style="color:lime;background:#111;padding:10px;font-size:11px;max-height:400px;overflow:auto"></pre>
<script>
var v=document.getElementById('v'),lg=document.getElementById('log');
function L(m){lg.textContent+=m+'\n';}
if(typeof Hls!=='undefined'&&Hls.isSupported()){
  L('hls.js v'+Hls.version);
  var h=new Hls();h.loadSource('/test.m3u8');h.attachMedia(v);
  h.on(Hls.Events.MANIFEST_PARSED,function(){L('MANIFEST OK');v.play();});
  h.on(Hls.Events.FRAG_LOADING,function(e,d){L('LOAD sn='+d.frag.sn);});
  h.on(Hls.Events.FRAG_LOADED,function(e,d){L('DONE sn='+d.frag.sn);});
  h.on(Hls.Events.ERROR,function(e,d){L('ERR '+d.details+' fatal='+d.fatal);});
}else if(v.canPlayType('application/vnd.apple.mpegurl')){
  L('Native HLS');v.src='/test.m3u8';v.play();
  v.addEventListener('error',function(){L('ERR '+v.error.code+' '+(v.error.message||''));});
}else{L('No HLS');}
</script></body></html>`)
	})

	t.Log("http://localhost:8765 (garage)")
	log.Fatal(http.ListenAndServe(":8765", nil))
}
