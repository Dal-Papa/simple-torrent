package engine

import (
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
)

type Torrent struct {
	sync.Mutex

	//anacrolix/torrent
	InfoHash   string
	Name       string
	Magnet     string
	Loaded     bool
	Downloaded int64
	Uploaded   int64
	Size       int64
	Files      []*File

	//cloud torrent
	Stats          *torrent.TorrentStats
	Started        bool
	Done           bool
	DoneCmdCalled  bool
	IsQueueing     bool
	IsSeeding      bool
	ManualStarted  bool
	IsAllFilesDone bool
	Percent        float32
	DownloadRate   float32
	UploadRate     float32
	SeedRatio      float32
	AddedAt        time.Time
	StartedAt      time.Time
	FinishedAt     time.Time
	StoppedAt      time.Time
	updatedAt      time.Time
	t              *torrent.Torrent
	e              *Engine
	dropWait       chan struct{}
	cld            Server
}

type File struct {
	//anacrolix/torrent
	Path          string
	Size          int64
	Completed     int64
	Done          bool
	DoneCmdCalled bool
	//cloud torrent
	Started bool
	Percent float32
	f       *torrent.File
}

// Update retrive info from torrent.Torrent
func (t *Torrent) updateOnGotInfo(tt *torrent.Torrent) {

	if tt.Info() != nil && !t.Loaded {
		t.t = tt
		t.Name = tt.Name()
		t.Loaded = true
		t.updateFileStatus()
		t.updateTorrentStatus()
		t.updateConnStat()

		if t.Magnet == "" {
			meta := tt.Metainfo()
			if magnet, err := meta.MagnetV2(); err == nil {
				t.Magnet = magnet.String()
			} else {
				t.Magnet = "ERROR{}"
			}
			t.Name = tt.Name()
		}
	}
}

func (t *Torrent) updateConnStat() {
	now := time.Now()
	lastStat := t.Stats
	curStat := t.t.Stats()

	if lastStat == nil {
		t.updatedAt = now
		t.Stats = &curStat
		return
	}

	bRead := curStat.BytesReadUsefulData.Int64()
	bWrite := curStat.BytesWrittenData.Int64()

	lRead := lastStat.BytesReadUsefulData.Int64()
	lWrite := lastStat.BytesWrittenData.Int64()

	// download will stop if t is done (bRead equals)
	if bRead >= lRead || bWrite > lWrite {

		// calculate ratio
		if bRead > 0 {
			t.SeedRatio = float32(bWrite) / float32(bRead)
		} else if t.Done {
			t.SeedRatio = float32(bWrite) / float32(t.Size)
		}

		// calculate rate
		dtinv := float32(time.Second) / float32(now.Sub(t.updatedAt))

		dldb := float32(bRead - lRead)
		t.DownloadRate = dldb * dtinv

		uldb := float32(bWrite - lWrite)
		t.UploadRate = uldb * dtinv

		t.Downloaded = t.t.BytesCompleted()
		t.Uploaded = bWrite
		t.updatedAt = now
		t.Stats = &curStat
	}
}

func (t *Torrent) updateFileStatus() {
	if t.IsAllFilesDone {
		return
	}

	tfiles := t.t.Files()
	if len(tfiles) > 0 && t.Files == nil {
		t.Files = make([]*File, len(tfiles))
	}

	//merge in files
	doneFlag := true
	for i, f := range tfiles {
		path := f.Path()
		file := t.Files[i]
		if file == nil {
			file = &File{Path: path, Started: t.Started, f: f}
			t.Files[i] = file
		}

		file.Size = f.Length()
		file.Completed = f.BytesCompleted()
		file.Percent = percent(file.Completed, file.Size)
		file.Done = file.Completed == file.Size
		if file.Done && !file.DoneCmdCalled {
			file.DoneCmdCalled = true
			go t.callDoneCmd(file.Path, "file", file.Size)
		}
		if !file.Done {
			doneFlag = false
		}
	}

	t.IsAllFilesDone = doneFlag
}

func (t *Torrent) updateTorrentStatus() {
	t.Size = t.t.Length()
	t.Percent = percent(t.t.BytesCompleted(), t.Size)
	t.Done = t.t.BytesMissing() == 0
	t.IsSeeding = t.t.Seeding() && t.Done

	// this process called at least on second Update calls
	if t.Done && !t.DoneCmdCalled {
		t.DoneCmdCalled = true
		t.FinishedAt = time.Now()
		log.Println("[TaskFinished]", t.InfoHash)
		go t.callDoneCmd(t.Name, "t", t.Size)
	}
}

func percent(n, total int64) float32 {
	if total == 0 {
		return float32(0)
	}
	return float32(int(float64(10000)*(float64(n)/float64(total)))) / 100
}

func (t *Torrent) callDoneCmd(name, tasktype string, size int64) {

	if cmd, env, err := t.e.config.GetCmdConfig(); err == nil {
		cmd := exec.Command(cmd)
		ih := t.InfoHash
		cmd.Env = append(env,
			fmt.Sprintf("CLD_RESTAPI=%s", t.cld.GetStrAttribute("RestAPI")),
			fmt.Sprintf("CLD_PATH=%s", name),
			fmt.Sprintf("CLD_HASH=%s", ih),
			fmt.Sprintf("CLD_TYPE=%s", tasktype),
			fmt.Sprintf("CLD_SIZE=%d", size),
			fmt.Sprintf("CLD_STARTTS=%d", t.StartedAt.Unix()),
			fmt.Sprintf("CLD_FILENUM=%d", len(t.Files)),
		)
		sout, _ := cmd.StdoutPipe()
		serr, _ := cmd.StderrPipe()
		log.Printf("[DoneCmd:%s]%sCMD:`%s' ENV:%s", tasktype, ih, cmd.String(), cmd.Env)
		if err := cmd.Start(); err != nil {
			log.Printf("[DoneCmd:%s]%sERR: %v", tasktype, ih, err)
			return
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go cmdScanLine(sout, &wg, fmt.Sprintf("[DoneCmd:%s]%sO:", log.filteredArg(tasktype, ih)...))
		go cmdScanLine(serr, &wg, fmt.Sprintf("[DoneCmd:%s]%sE:", log.filteredArg(tasktype, ih)...))
		wg.Wait()

		// call Wait will close pipes above
		if err := cmd.Wait(); err != nil {
			log.Printf("[DoneCmd:%s]%sERR: %v", tasktype, ih, err)
			return
		}

		log.Printf("[DoneCmd:%s]%sExit code: %d", tasktype, ih, cmd.ProcessState.ExitCode())
	} else {
		log.Println("[DoneCmd]", t.InfoHash, err)
	}
}
