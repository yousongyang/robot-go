package master

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------- DBTool API Handlers ----------

func (m *Master) handleDBToolStatus(w http.ResponseWriter, _ *http.Request) {
	if m.dbtoolMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"enabled": false,
		})
		return
	}
	session := m.dbtoolMgr.GetSession()
	lastAt, lastErr := m.dbtoolMgr.GetLastReloadInfo()
	var lastAtStr string
	if !lastAt.IsZero() {
		lastAtStr = lastAt.Format("2006-01-02 15:04:05")
	}
	resp := map[string]interface{}{
		"enabled":           true,
		"connected":         session != nil,
		"last_reload_at":    lastAtStr,
		"last_reload_error": lastErr,
		"config": map[string]interface{}{
			"pb_file":       m.dbtoolMgr.config.PBFile,
			"redis_config":  m.dbtoolMgr.config.RedisConfig,
			"record_prefix": m.dbtoolMgr.config.RecordPrefix,
		},
	}
	if session != nil {
		resp["tables"] = session.ListTables()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (m *Master) handleDBToolTables(w http.ResponseWriter, _ *http.Request) {
	if m.dbtoolMgr == nil {
		http.Error(w, "dbtool not enabled", http.StatusServiceUnavailable)
		return
	}
	session := m.dbtoolMgr.GetSession()
	if session == nil {
		http.Error(w, "dbtool not connected", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, session.ListTables())
}

func (m *Master) handleDBToolQuery(w http.ResponseWriter, r *http.Request) {
	if m.dbtoolMgr == nil {
		http.Error(w, "dbtool not enabled", http.StatusServiceUnavailable)
		return
	}
	session := m.dbtoolMgr.GetSession()
	if session == nil {
		http.Error(w, "dbtool not connected", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Table     string   `json:"table"`
		Index     string   `json:"index"`
		KeyValues []string `json:"key_values"`
		ExtraArgs []string `json:"extra_args,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	result, err := session.ExecuteQuery(req.Table, req.Index, req.KeyValues, req.ExtraArgs)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"error":  err.Error(),
			"result": "",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"result": result,
	})
}

// ---------- DBTool Presets Handlers ----------

func (m *Master) handleDBToolReload(w http.ResponseWriter, _ *http.Request) {
	if m.dbtoolMgr == nil {
		http.Error(w, "dbtool not enabled", http.StatusServiceUnavailable)
		return
	}
	if err := m.dbtoolMgr.Reload(); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	session := m.dbtoolMgr.GetSession()
	var tables interface{}
	if session != nil {
		tables = session.ListTables()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"tables": tables,
	})
}

func (m *Master) handleDBToolListPresets(w http.ResponseWriter, _ *http.Request) {
	if m.dbtoolMgr == nil {
		writeJSON(w, http.StatusOK, []DBToolPreset{})
		return
	}
	presets, err := m.dbtoolMgr.ListPresets()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, presets)
}

func (m *Master) handleDBToolSavePreset(w http.ResponseWriter, r *http.Request) {
	if m.dbtoolMgr == nil {
		http.Error(w, "dbtool not enabled", http.StatusServiceUnavailable)
		return
	}
	var preset DBToolPreset
	if err := json.NewDecoder(r.Body).Decode(&preset); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if preset.Name == "" || preset.Table == "" || preset.Index == "" {
		http.Error(w, "name, table, and index are required", http.StatusBadRequest)
		return
	}
	if err := m.dbtoolMgr.SavePreset(preset); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (m *Master) handleDBToolDeletePreset(w http.ResponseWriter, r *http.Request) {
	if m.dbtoolMgr == nil {
		http.Error(w, "dbtool not enabled", http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if err := m.dbtoolMgr.DeletePreset(name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleDBToolUploadPB 接收上传的 .pb 文件，原子覆盖配置指定的路径，并自动触发 Reload
func (m *Master) handleDBToolUploadPB(w http.ResponseWriter, r *http.Request) {
	if m.dbtoolMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"ok": false, "error": "dbtool not enabled"})
		return
	}

	// 限制上传大小：最大 64 MB
	const maxUploadSize = 64 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "file too large or bad multipart: " + err.Error()})
		return
	}

	file, header, err := r.FormFile("pb_file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "missing form field \"pb_file\": " + err.Error()})
		return
	}
	defer file.Close()

	// 只允许 .pb 文件
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".pb") {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "only .pb files are allowed"})
		return
	}

	destPath := m.dbtoolMgr.config.PBFile
	if destPath == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": "pb_file path not configured"})
		return
	}

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": "cannot create destination directory: " + err.Error()})
		return
	}

	// 写入临时文件，再原子重命名，防止写入中被读取
	tmpPath := fmt.Sprintf("%s.upload.%d.tmp", destPath, time.Now().UnixNano())
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": "cannot create temp file: " + err.Error()})
		return
	}
	written, copyErr := io.Copy(tmpFile, file)
	tmpFile.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": "write failed: " + copyErr.Error()})
		return
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": "rename failed: " + err.Error()})
		return
	}

	// 触发 Reload
	reloadErr := m.dbtoolMgr.Reload()
	if reloadErr != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      false,
			"saved":   true,
			"written": written,
			"dest":    filepath.Base(destPath),
			"error":   reloadErr.Error(),
		})
		return
	}
	session := m.dbtoolMgr.GetSession()
	tableCount := 0
	if session != nil {
		tableCount = len(session.ListTables())
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"saved":       true,
		"written":     written,
		"dest":        filepath.Base(destPath),
		"table_count": tableCount,
	})
}
