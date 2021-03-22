package mmodule

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/vxcontrol/luar"
	"github.com/vxcontrol/vxcommon/agent"
	"github.com/vxcontrol/vxcommon/controller"
	"github.com/vxcontrol/vxcommon/db"
	"github.com/vxcontrol/vxcommon/loader"
	"github.com/vxcontrol/vxcommon/lua"
	"github.com/vxcontrol/vxcommon/utils"
	"github.com/vxcontrol/vxcommon/vxproto"
)

// List of SQL queries string
const (
	sIsAgentExistSQL      string = "SELECT `hash` FROM `agents` WHERE `status` IN ('created', 'connected', 'disconnected') AND `hash` = ? LIMIT 1"
	sSetAgentConnected    string = "UPDATE `agents` SET `status`='connected',`ip`=?,`connected_date`=NOW() WHERE `hash` = ?"
	sSetAgentDisconnected string = "UPDATE `agents` SET `status`='disconnected' WHERE `hash` = ?"
	sSetAgentInfo         string = "UPDATE `agents` SET `info`=? WHERE `hash` = ?"
	sGetAgentIDByHash     string = "SELECT `id` FROM `agents` WHERE `hash` = ? LIMIT 1"
	sGetModuleIDByName    string = "SELECT `id` FROM `modules` WHERE `name` = ? AND `agent_id` IN (0, ?) ORDER BY `agent_id` DESC LIMIT 1"
	sCheckEventExist      string = "SELECT `id` FROM `events` WHERE `uniq` = ? LIMIT 1"
	sPushEvent            string = "INSERT INTO `events` (`agent_id`,`module_id`,`info`,`date`) VALUES(?, ?, ?, NOW())"
	sUpdateEvent          string = "UPDATE `events` SET `info`=?,`date`=NOW() WHERE `uniq` = ?"
)

// MainModule is struct which contains full state for agent working
type MainModule struct {
	proto      vxproto.IVXProto
	cnt        controller.IController
	dbc        *db.DB
	agents     *agentList
	states     *stateList
	listen     string
	socket     vxproto.IModuleSocket
	wgControl  sync.WaitGroup
	wgReceiver sync.WaitGroup
	mutexReq   *sync.Mutex
	mutexAgent *sync.Mutex
}

type agentInfo struct {
	info   *vxproto.AgentInfo
	quit   chan struct{}
	update chan struct{}
	done   chan struct{}
}

type agentList struct {
	list  map[string]*agentInfo
	mutex *sync.Mutex
}

func (agents *agentList) dump() map[string]*agentInfo {
	agents.mutex.Lock()
	defer agents.mutex.Unlock()

	return agents.list
}

func (agents *agentList) dumpID(id string) map[string]*agentInfo {
	agents.mutex.Lock()
	defer agents.mutex.Unlock()

	nlist := make(map[string]*agentInfo)
	for token, info := range agents.list {
		if info.info.ID == id {
			nlist[token] = info
		}
	}

	return nlist
}

func (agents *agentList) add(token string, info *agentInfo) {
	agents.mutex.Lock()
	defer agents.mutex.Unlock()

	agents.list[token] = info
}

func (agents *agentList) get(token string) *agentInfo {
	agents.mutex.Lock()
	defer agents.mutex.Unlock()

	if info, ok := agents.list[token]; ok {
		return info
	}

	return nil
}

func (agents *agentList) del(token string) {
	agents.mutex.Lock()
	defer agents.mutex.Unlock()

	delete(agents.list, token)
}

type stateInfo struct {
	quit   chan struct{}
	update chan struct{}
	done   chan struct{}
}

type stateList struct {
	list  map[string]*stateInfo
	mutex *sync.Mutex
}

func (states *stateList) dump() map[string]*stateInfo {
	states.mutex.Lock()
	defer states.mutex.Unlock()

	return states.list
}

func (states *stateList) add(id string, info *stateInfo) {
	states.mutex.Lock()
	defer states.mutex.Unlock()

	states.list[id] = info
}

func (states *stateList) get(id string) *stateInfo {
	states.mutex.Lock()
	defer states.mutex.Unlock()

	if info, ok := states.list[id]; ok {
		return info
	}

	return nil
}

func (states *stateList) del(id string) {
	states.mutex.Lock()
	defer states.mutex.Unlock()

	delete(states.list, id)
}

// OnConnect is function that control hanshake on server
func (mm *MainModule) OnConnect(socket vxproto.IAgentSocket) (err error) {
	pubInfo := socket.GetPublicInfo()
	logrus.WithFields(logrus.Fields{
		"module": "main",
		"id":     pubInfo.ID,
		"type":   pubInfo.Type.String(),
		"src":    pubInfo.Src,
		"dst":    pubInfo.Dst,
	}).Info("vxserver: connect")
	switch socket.GetPublicInfo().Type {
	case vxproto.VXAgent:
		err = utils.DoHandshakeWithAgentOnServer(socket)
	case vxproto.Browser:
		err = utils.DoHandshakeWithBrowserOnServer(socket)
	default:
		err = errors.New("unknown client type")
	}
	if err != nil {
		logrus.WithError(err).Error("vxserver: connect error")
	}

	return
}

// HasAgentIDValid is function that validate AgentID and AgentType in whitelist
func (mm *MainModule) HasAgentIDValid(agentID string, agentType vxproto.AgentType) bool {
	if mm.dbc == nil {
		return agentID != ""
	}

	rows, err := mm.dbc.Query(sIsAgentExistSQL, agentID)
	if err != nil || len(rows) == 0 {
		return false
	}

	// Deny second connection from VXAgent with the same agent ID
	if agentType == vxproto.VXAgent {
		for _, info := range mm.agents.dumpID(agentID) {
			if info.info.Type == vxproto.VXAgent {
				return false
			}
		}
	}

	return true
}

// HasAgentInfoValid is function that validate Agent Information in Agent list
func (mm *MainModule) HasAgentInfoValid(agentID string, info *agent.Information) bool {
	if !utils.HasInfoValid(info) {
		return false
	}

	if mm.dbc != nil {
		jinfo, err := json.Marshal(info)
		if err == nil {
			mm.dbc.Exec(sSetAgentInfo, jinfo, agentID)
		}
	}

	return true
}

// RegisterLuaAPI is function that registrate extra API function for each of type service
func (mm *MainModule) RegisterLuaAPI(state *lua.State, config *loader.ModuleConfig) error {
	id := config.AgentID
	mname := config.Name

	luar.Register(state.L, "__api", luar.Map{
		"push_event": func(aid, info string) bool {
			if id != "" && aid != id {
				return false
			}
			if mm.dbc != nil {
				type sInfo struct {
					Uniq string `json:"uniq"`
				}
				var agentID, moduleID int
				var uniq string
				var si sInfo

				if err := json.Unmarshal([]byte(info), &si); err == nil {
					uniq = si.Uniq
				}

				if uniq != "" {
					rows, err := mm.dbc.Query(sCheckEventExist, uniq)
					if err == nil && len(rows) == 1 {
						if _, err = mm.dbc.Exec(sUpdateEvent, info, uniq); err == nil {
							return true
						}
					}
				}

				if aid != "" {
					rows, err := mm.dbc.Query(sGetAgentIDByHash, aid)
					if err != nil || len(rows) == 0 {
						return false
					}
					if val, ok := rows[0]["id"]; ok {
						if agentID, err = strconv.Atoi(val); err != nil {
							return false
						}
					}
				}

				rows, err := mm.dbc.Query(sGetModuleIDByName, mname, agentID)
				if err != nil || len(rows) == 0 {
					return false
				}
				if val, ok := rows[0]["id"]; ok {
					if moduleID, err = strconv.Atoi(val); err != nil {
						return false
					}
				}

				if _, err = mm.dbc.Exec(sPushEvent, agentID, moduleID, info); err == nil {
					return true
				}
			}
			return false
		},
	})
	luar.GoToLua(state.L, id)
	state.L.SetGlobal("__id")

	return nil
}

// UnregisterLuaAPI is function that unregistrate extra API function for each of type service
func (mm *MainModule) UnregisterLuaAPI(state *lua.State, config *loader.ModuleConfig) error {
	luar.Register(state.L, "__api", luar.Map{})

	return nil
}

// DefaultRecvPacket is function that operate packets as default receiver
func (mm *MainModule) DefaultRecvPacket(packet *vxproto.Packet) error {
	logrus.WithFields(logrus.Fields{
		"module": packet.Module,
		"type":   packet.PType.String(),
		"src":    packet.Src,
		"dst":    packet.Dst,
	}).Debug("vxserver: default receiver got new packet")

	return nil
}

func (mm *MainModule) recvData(src string, data *vxproto.Data) error {
	logrus.WithFields(logrus.Fields{
		"module": "main",
		"type":   "data",
		"src":    src,
		"len":    len(data.Data),
	}).Debug("vxserver: received data")

	return nil
}

func (mm *MainModule) recvFile(src string, file *vxproto.File) error {
	logrus.WithFields(logrus.Fields{
		"module": "main",
		"type":   "file",
		"name":   file.Name,
		"path":   file.Path,
		"uniq":   file.Uniq,
		"src":    src,
	}).Debug("vxserver: received file")

	return nil
}

func (mm *MainModule) recvText(src string, text *vxproto.Text) error {
	logrus.WithFields(logrus.Fields{
		"module": "main",
		"type":   "text",
		"name":   text.Name,
		"len":    len(text.Data),
		"src":    src,
	}).Debug("vxserver: received text")

	return nil
}

func (mm *MainModule) recvMsg(src string, msg *vxproto.Msg) error {
	logrus.WithFields(logrus.Fields{
		"module": "main",
		"type":   "msg",
		"msg":    msg.MType.String(),
		"len":    len(msg.Data),
		"src":    src,
	}).Debug("vxserver: received message")

	return nil
}

func (mm *MainModule) checkModulesForAgent(dst string, mIDs []string) []string {
	var nmIDs []string
	agentInfo := mm.agents.get(dst)
	if agentInfo == nil {
		return nmIDs
	}

	stringInSlice := func(a string, list []string) bool {
		for _, b := range list {
			if b == a {
				return true
			}
		}
		return false
	}

	for _, mID := range mIDs {
		mConfig := mm.cnt.GetModule(mID).GetConfig()
		agentOS := agentInfo.info.Info.GetOs()
		if archList, ok := mConfig.OS[agentOS.GetType()]; ok && stringInSlice(agentOS.GetArch(), archList) {
			nmIDs = append(nmIDs, mID)
		}
	}

	return nmIDs
}

func (mm *MainModule) syncModules(id, dst string, mStatusList *agent.ModuleStatusList) (*agent.ModuleStatusList, error) {
	var err error
	var (
		wantStartModuleIDs  []string
		wantStopModuleIDs   []string
		wantUpdateModuleIDs []string
		wantUpdateConfigIds []string
	)
	mIDs := append(mm.cnt.GetModuleIdsForAgent(id), mm.cnt.GetSharedModuleIds()...)
	mIDs = mm.checkModulesForAgent(dst, mIDs)
	mStates := mm.cnt.GetModuleStates(mIDs)
	mObjs := mm.cnt.GetModules(mIDs)

	isEqualModuleConfig := func(mc1 *loader.ModuleConfig, mc2 *agent.Config) bool {
		if mc1.LastUpdate != mc2.GetLastUpdate() || mc1.Version != mc2.GetVersion() {
			return false
		}
		if len(mc1.OS) != len(mc2.GetOs()) || len(mc1.Events) != len(mc2.GetEvents()) {
			return false
		}

		for _, os := range mc2.GetOs() {
			if arch, ok := mc1.OS[os.GetType()]; !ok || len(arch) != len(os.Arch) {
				return false
			}
		}

		return true
	}

	isEqualModuleConfigItem := func(mc1 *loader.ModuleConfig, mc2 *agent.ConfigItem) bool {
		if mc1.GetCurrentConfig() != mc2.GetCurrentConfig() ||
			mc1.GetDefaultConfig() != mc2.GetDefaultConfig() ||
			mc1.GetConfigSchema() != mc2.GetConfigSchema() ||
			mc1.GetEventDataSchema() != mc2.GetEventDataSchema() ||
			mc1.GetEventConfigSchema() != mc2.GetEventConfigSchema() ||
			mc1.GetCurrentEventConfig() != mc2.GetCurrentEventConfig() ||
			mc1.GetDefaultEventConfig() != mc2.GetDefaultEventConfig() {
			return false
		}
		return true
	}

	for _, mStatusItem := range mStatusList.GetList() {
		mConfig := mStatusItem.GetConfig()
		mConfigItem := mStatusItem.GetConfigItem()
		mID := mConfig.GetAgentId() + ":" + mConfig.GetName()
		if state, ok := mStates[mID]; ok && state.GetStatus() == agent.ModuleStatus_RUNNING {
			if obj, ok := mObjs[mID]; ok {
				if !isEqualModuleConfig(obj.GetConfig(), mConfig) ||
					!isEqualModuleConfigItem(obj.GetConfig(), mConfigItem) {
					wantUpdateModuleIDs = append(wantUpdateModuleIDs, mID)
				}
			}
			// TODO: here should be exception if module object not found
		} else {
			wantStopModuleIDs = append(wantStopModuleIDs, mID)
		}
	}

	for _, mID := range mIDs {
		var mStatus *agent.ModuleStatus
		for _, mStatusItem := range mStatusList.GetList() {
			mConfig := mStatusItem.GetConfig()
			if mID == mConfig.GetAgentId()+":"+mConfig.GetName() {
				mStatus = mStatusItem
				break
			}
		}
		if mStatus == nil {
			wantStartModuleIDs = append(wantStartModuleIDs, mID)
		}
	}

	if len(wantStopModuleIDs) != 0 {
		if mStatusList, err = mm.stopModules(dst, wantStopModuleIDs); err != nil {
			return nil, err
		}
	}
	if len(wantStartModuleIDs) != 0 {
		if mStatusList, err = mm.startModules(dst, wantStartModuleIDs); err != nil {
			return nil, err
		}
	}
	if len(wantUpdateModuleIDs) != 0 {
		if mStatusList, err = mm.updateModules(dst, wantUpdateModuleIDs); err != nil {
			return nil, err
		}
	}
	if len(wantUpdateConfigIds) != 0 {
		if mStatusList, err = mm.updateModulesConfig(dst, wantUpdateConfigIds); err != nil {
			return nil, err
		}
	}

	return mStatusList, nil
}

func (mm *MainModule) controlBrowser(dst string, syncRun chan struct{}) {
	agent := mm.agents.get(dst)
	defer mm.wgControl.Done()
	defer func() { agent.done <- struct{}{} }()
	defer mm.agents.del(dst)

	mm.cnt.SetUpdateChanForAgent(agent.info.ID, agent.update)
	syncRun <- struct{}{}
	for {
		select {
		case <-agent.update:
			continue
		case <-agent.quit:
			break
		}

		break
	}
}

func (mm *MainModule) controlAgent(dst string, syncRun chan struct{}) {
	var wgSync sync.WaitGroup
	mxSync := &sync.Mutex{}
	agent := mm.agents.get(dst)
	defer mm.wgControl.Done()
	defer func() { agent.done <- struct{}{} }()

	mm.cnt.SetUpdateChanForAgent(agent.info.ID, agent.update)
	syncRun <- struct{}{}
	for {
		wgSync.Add(1)
		// TODO: there is a need to get more guarantee that syncModules was successful
		go func() {
			defer wgSync.Done()
			defer mxSync.Unlock()
			mxSync.Lock()

			mStatusList, err := mm.getStatusModules(dst)
			if err != nil {
				return
			}

			if mStatusList, err = mm.syncModules(agent.info.ID, dst, mStatusList); err != nil {
				return
			}
		}()

		select {
		case <-agent.update:
			continue
		case <-agent.quit:
			break
		}

		break
	}

	mm.agents.del(dst)
	wgSync.Wait()
}

func (mm *MainModule) controlState(id string, syncRun chan struct{}) {
	state := mm.states.get(id)
	defer mm.wgControl.Done()
	defer func() { state.done <- struct{}{} }()
	defer mm.states.del(id)
	defer mm.cnt.StopModulesForAgent(id)

	if _, err := mm.cnt.StartModulesForAgent(id); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"agent_id": id,
		}).Error("can't start modules for agent")
	}

	mm.cnt.SetUpdateChanForAgent(id, state.update)
	syncRun <- struct{}{}
	for {
		select {
		case <-state.update:
			continue
		case <-state.quit:
			break
		}

		break
	}
}

func (mm *MainModule) controlSharedState(syncRun chan struct{}) {
	state := mm.states.get("")
	defer mm.wgControl.Done()
	defer func() { state.done <- struct{}{} }()
	defer mm.states.del("")
	defer mm.cnt.StopSharedModules()
	defer mm.cnt.UnsetUpdateChan(state.update)

	if _, err := mm.cnt.StartSharedModules(); err != nil {
		logrus.WithError(err).Error("can't start shared modules")
	}

	mm.cnt.SetUpdateChan(state.update)
	syncRun <- struct{}{}
	for {
		select {
		case <-state.update:
			continue
		case <-state.quit:
			break
		}

		break
	}
}

func (mm *MainModule) handlerAgentConnected(info *vxproto.AgentInfo) {
	mm.mutexAgent.Lock()
	defer mm.mutexAgent.Unlock()

	agentControlSync := make(chan struct{})
	mm.agents.add(info.Dst, &agentInfo{
		info:   info,
		quit:   make(chan struct{}),
		update: make(chan struct{}),
		done:   make(chan struct{}),
	})

	if info.Type == vxproto.VXAgent {
		if mm.dbc != nil {
			mm.dbc.Exec(sSetAgentConnected, info.IP, info.ID)
		}
	}

	if mm.states.get(info.ID) == nil {
		stateControlSync := make(chan struct{})
		mm.states.add(info.ID, &stateInfo{
			quit:   make(chan struct{}),
			update: make(chan struct{}),
			done:   make(chan struct{}),
		})
		mm.wgControl.Add(1)
		go mm.controlState(info.ID, stateControlSync)
		defer func() { <-stateControlSync }()
	}

	mm.wgControl.Add(1)
	switch info.Type {
	case vxproto.Browser:
		go mm.controlBrowser(info.Dst, agentControlSync)
		defer func() { <-agentControlSync }()
	case vxproto.VXAgent:
		go mm.controlAgent(info.Dst, agentControlSync)
		defer func() { <-agentControlSync }()
	default:
		mm.wgControl.Done()
	}
}

func (mm *MainModule) handlerAgentDisconnected(info *vxproto.AgentInfo) {
	mm.mutexAgent.Lock()
	defer mm.mutexAgent.Unlock()

	agentInfo := mm.agents.get(info.Dst)
	if agentInfo != nil {
		mm.cnt.UnsetUpdateChanForAgent(info.ID, agentInfo.update)
		agentInfo.quit <- struct{}{}
		<-agentInfo.done
	}

	if info.Type == vxproto.VXAgent {
		if mm.dbc != nil {
			mm.dbc.Exec(sSetAgentDisconnected, info.ID)
		}
	}

	stateInfo := mm.states.get(info.ID)
	if len(mm.agents.dumpID(info.ID)) == 0 && stateInfo != nil {
		mm.cnt.UnsetUpdateChanForAgent(info.ID, stateInfo.update)
		stateInfo.quit <- struct{}{}
		<-stateInfo.done
	}
}

func (mm *MainModule) handlerStopMainModule() {
	for id, stateInfo := range mm.states.dump() {
		mm.cnt.UnsetUpdateChanForAgent(id, stateInfo.update)
		stateInfo.quit <- struct{}{}
		<-stateInfo.done
	}
	mm.wgControl.Wait()
}

func (mm *MainModule) recvPacket() error {
	defer mm.wgReceiver.Done()

	receiver := mm.socket.GetReceiver()
	if receiver == nil {
		logrus.Error("vxserver: failed to initialize packet receiver")
		return errors.New("failed to initialize packet receiver")
	}
	getAgentEntry := func(agentInfo *vxproto.AgentInfo) *logrus.Entry {
		return logrus.WithFields(logrus.Fields{
			"id":   agentInfo.ID,
			"type": agentInfo.Type.String(),
			"ip":   agentInfo.IP,
			"src":  agentInfo.Src,
			"dst":  agentInfo.Dst,
		})
	}
	for {
		packet := <-receiver
		if packet == nil {
			return errors.New("failed receive packet")
		}
		switch packet.PType {
		case vxproto.PTData:
			mm.recvData(packet.Src, packet.GetData())
		case vxproto.PTFile:
			mm.recvFile(packet.Src, packet.GetFile())
		case vxproto.PTText:
			mm.recvText(packet.Src, packet.GetText())
		case vxproto.PTMsg:
			mm.recvMsg(packet.Src, packet.GetMsg())
		case vxproto.PTControl:
			msg := packet.GetControlMsg()
			switch msg.MsgType {
			case vxproto.AgentConnected:
				getAgentEntry(msg.AgentInfo).Info("vxserver: agent connected")
				mm.handlerAgentConnected(msg.AgentInfo)
				getAgentEntry(msg.AgentInfo).Info("vxserver: agent connected done")
			case vxproto.AgentDisconnected:
				getAgentEntry(msg.AgentInfo).Info("vxserver: agent disconnected")
				mm.handlerAgentDisconnected(msg.AgentInfo)
				getAgentEntry(msg.AgentInfo).Info("vxserver: agent disconnected done")
			case vxproto.StopModule:
				logrus.Info("vxserver: got signal to stop main module")
				mm.handlerStopMainModule()
				logrus.Info("vxserver: got signal to stop main module done")
				return nil
			}
		default:
			logrus.Error("vxserver: got packet has unexpected packet type")
			return errors.New("unexpected packet type")
		}
	}
}

// New is function which constructed MainModule object
func New(listen string, cl controller.IConfigLoader, fl controller.IFilesLoader, db *db.DB) (*MainModule, error) {
	mm := &MainModule{
		dbc:    db,
		listen: listen,
		agents: &agentList{
			list:  make(map[string]*agentInfo),
			mutex: &sync.Mutex{},
		},
		states: &stateList{
			list:  make(map[string]*stateInfo),
			mutex: &sync.Mutex{},
		},
		mutexReq:   &sync.Mutex{},
		mutexAgent: &sync.Mutex{},
	}

	mm.proto = vxproto.New(mm)
	if mm.proto == nil {
		return nil, errors.New("failed initialize VXProto object")
	}

	mm.cnt = controller.NewController(mm, cl, fl, mm.proto)
	if err := mm.cnt.Load(); err != nil {
		return nil, err
	}

	mm.socket = mm.proto.NewModule("main", "")
	if mm.socket == nil {
		return nil, errors.New("failed initialize main module into VXProto")
	}

	return mm, nil
}

// Start is function which execute main logic of MainModule
func (mm *MainModule) Start() error {
	if mm.proto == nil {
		return errors.New("VXProto didn't initialized")
	}
	if mm.socket == nil {
		return errors.New("module socket didn't initialized")
	}
	if mm.cnt == nil {
		return errors.New("controller didn't initialized")
	}

	if !mm.proto.AddModule(mm.socket) {
		return errors.New("failed module socket register")
	}

	// Run main handler of packets
	mm.wgReceiver.Add(1)
	// TODO: here need synchronization with running listener
	go mm.recvPacket()

	stateControlSync := make(chan struct{})
	mm.states.add("", &stateInfo{
		quit:   make(chan struct{}),
		update: make(chan struct{}),
	})
	mm.wgControl.Add(1)
	go mm.controlSharedState(stateControlSync)
	<-stateControlSync

	config := map[string]string{"listen": mm.listen}
	return mm.proto.Listen(config)
}

// Stop is function which stop main logic of MainModule
func (mm *MainModule) Stop() error {
	if mm.proto == nil {
		return errors.New("VXProto didn't initialized")
	}
	if mm.socket == nil {
		return errors.New("module socket didn't initialize")
	}
	if mm.cnt == nil {
		return errors.New("controller didn't initialized")
	}

	if !mm.proto.DelModule(mm.socket) {
		return errors.New("failed to delete module socket")
	}

	if err := mm.cnt.Close(); err != nil {
		return errors.New("modules didn't stop. Error: " + err.Error())
	}

	receiver := mm.socket.GetReceiver()
	if receiver != nil {
		receiver <- &vxproto.Packet{
			PType: vxproto.PTControl,
			Payload: &vxproto.ControlMessage{
				MsgType: vxproto.StopModule,
			},
		}
	}
	mm.wgReceiver.Wait()
	return mm.proto.Close()
}
