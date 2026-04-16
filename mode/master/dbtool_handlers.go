package master

import (
	"encoding/json"
	"net/http"
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
	resp := map[string]interface{}{
		"enabled":   true,
		"connected": session != nil,
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
