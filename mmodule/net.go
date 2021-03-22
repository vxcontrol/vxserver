package mmodule

import (
	"errors"
	"strings"

	"github.com/golang/protobuf/proto"
	"github.com/vxcontrol/vxcommon/agent"
	"github.com/vxcontrol/vxcommon/loader"
	"github.com/vxcontrol/vxcommon/utils"
	"github.com/vxcontrol/vxcommon/vxproto"
)

// List of default values
const (
	defaultRecvResultTimeout = 10000
)

// requestAgent is function which do request and wait any unstructured result
func (mm *MainModule) requestAgent(dst string, msgType agent.Message_Type, payload []byte) (*agent.Message, error) {
	mm.mutexReq.Lock()
	defer mm.mutexReq.Unlock()

	if mm.socket == nil {
		return nil, errors.New("module Socket didn't initialize")
	}

	messageData, err := proto.Marshal(&agent.Message{
		Type:    msgType.Enum(),
		Payload: payload,
	})
	if err != nil {
		return nil, errors.New("error marshal request packet: " + err.Error())
	}

	if mm.agents.get(dst) == nil {
		return nil, errors.New("target agent not found")
	}

	data := &vxproto.Data{
		Data: messageData,
	}
	if err = mm.socket.SendDataTo(dst, data); err != nil {
		return nil, err
	}

	data, err = mm.socket.RecvDataFrom(dst, defaultRecvResultTimeout)
	if err != nil {
		return nil, err
	}

	var message agent.Message
	if err = proto.Unmarshal(data.Data, &message); err != nil {
		return nil, errors.New("error unmarshal response message packet: " + err.Error())
	}

	return &message, nil
}

// requestAgentAboutModules is function which do request and wait module status structure
func (mm *MainModule) requestAgentAboutModules(dst string, mIDs []string, msgType agent.Message_Type) (*agent.ModuleStatusList, error) {
	agentInfo := mm.agents.get(dst)
	if agentInfo == nil {
		return nil, errors.New("target agent not found")
	}

	payload, err := mm.getModuleListData(dst, mIDs)
	if err != nil {
		return nil, err
	}

	message, err := mm.requestAgent(dst, msgType, payload)
	if err != nil {
		return nil, err
	}

	var statusMessage agent.ModuleStatusList
	if err = proto.Unmarshal(message.Payload, &statusMessage); err != nil {
		return nil, errors.New("error unmarshal response packet of status modules: " + err.Error())
	}

	return &statusMessage, nil
}

// getModuleListData is function for generate ModuleList message
func (mm *MainModule) getModuleListData(dst string, mIDs []string) ([]byte, error) {
	moduleListMessage := mm.getModuleList(dst, mIDs)
	moduleListMessageData, err := proto.Marshal(moduleListMessage)
	if err != nil {
		return nil, errors.New("error marshal modules list packet: " + err.Error())
	}

	return moduleListMessageData, nil
}

// moduleToPB is function which convert module data to Protobuf structure
func (mm *MainModule) moduleToPB(dst string, mc *loader.ModuleConfig, mi *loader.ModuleItem) *agent.Module {
	var args []*agent.Module_Arg
	var files []*agent.Module_File
	var os, arch string
	agentInfo := mm.agents.get(dst)
	if agentInfo != nil {
		os = agentInfo.info.Info.GetOs().GetType()
		arch = agentInfo.info.Info.GetOs().GetArch()
	}

	var osList []*agent.Config_OS
	for osType, archList := range mc.OS {
		osList = append(osList, &agent.Config_OS{
			Type: utils.GetRef(osType),
			Arch: archList,
		})
	}
	config := &agent.Config{
		Os:         osList,
		AgentId:    utils.GetRef(mc.AgentID),
		Name:       utils.GetRef(mc.Name),
		Version:    utils.GetRef(mc.Version),
		Events:     mc.Events,
		LastUpdate: utils.GetRef(mc.LastUpdate),
	}

	iconfig := &agent.ConfigItem{
		ConfigSchema:       utils.GetRef(mc.GetConfigSchema()),
		DefaultConfig:      utils.GetRef(mc.GetDefaultConfig()),
		CurrentConfig:      utils.GetRef(mc.GetCurrentConfig()),
		EventDataSchema:    utils.GetRef(mc.GetEventDataSchema()),
		EventConfigSchema:  utils.GetRef(mc.GetEventConfigSchema()),
		DefaultEventConfig: utils.GetRef(mc.GetDefaultEventConfig()),
		CurrentEventConfig: utils.GetRef(mc.GetCurrentEventConfig()),
	}

	for path, data := range mi.GetFilesByFilter(os, arch) {
		files = append(files, &agent.Module_File{
			Path: utils.GetRef(path),
			Data: data,
		})
	}

	for key, value := range mi.GetArgs() {
		args = append(args, &agent.Module_Arg{
			Key:   utils.GetRef(key),
			Value: value,
		})
	}

	return &agent.Module{
		Name:       utils.GetRef(mc.Name),
		Config:     config,
		ConfigItem: iconfig,
		Args:       args,
		Files:      files,
	}
}

// getModuleList is API functions which execute remote logic
func (mm *MainModule) getModuleList(dst string, mIDs []string) *agent.ModuleList {
	var modules agent.ModuleList
	mObjs := mm.cnt.GetModules(mIDs)

	for _, mID := range mIDs {
		var module *agent.Module
		if mObj, ok := mObjs[mID]; ok {
			module = mm.moduleToPB(dst, mObj.GetConfig(), mObj.GetFiles().GetCModule())
		} else {
			module = &agent.Module{
				Name: utils.GetRef(strings.Split(mID, ":")[1]),
			}
		}
		modules.List = append(modules.List, module)
	}

	return &modules
}

// getInformation is API functions which execute remote logic
func (mm *MainModule) getInformation(dst string) (*agent.Information, error) {
	message, err := mm.requestAgent(dst, agent.Message_GET_INFORMATION, []byte{})
	if err != nil {
		return nil, err
	}

	var infoMessage agent.Information
	if err = proto.Unmarshal(message.Payload, &infoMessage); err != nil {
		return nil, errors.New("error unmarshal response packet of information: " + err.Error())
	}

	return &infoMessage, nil
}

// getStatusModules is API functions which execute remote logic
func (mm *MainModule) getStatusModules(dst string) (*agent.ModuleStatusList, error) {
	message, err := mm.requestAgent(dst, agent.Message_GET_STATUS_MODULES, []byte{})
	if err != nil {
		return nil, err
	}

	var statusMessage agent.ModuleStatusList
	if err = proto.Unmarshal(message.Payload, &statusMessage); err != nil {
		return nil, errors.New("error unmarshal response packet of status modules: " + err.Error())
	}

	return &statusMessage, nil
}

// startModules is API functions which execute remote logic
func (mm *MainModule) startModules(dst string, mIDs []string) (*agent.ModuleStatusList, error) {
	return mm.requestAgentAboutModules(dst, mIDs, agent.Message_START_MODULES)
}

// stopModules is API functions which execute remote logic
func (mm *MainModule) stopModules(dst string, mIDs []string) (*agent.ModuleStatusList, error) {
	return mm.requestAgentAboutModules(dst, mIDs, agent.Message_STOP_MODULES)
}

// updateModules is API functions which execute remote logic
func (mm *MainModule) updateModules(dst string, mIDs []string) (*agent.ModuleStatusList, error) {
	return mm.requestAgentAboutModules(dst, mIDs, agent.Message_UPDATE_MODULES)
}

// updateModulesConfig is API functions which execute remote logic
func (mm *MainModule) updateModulesConfig(dst string, mIDs []string) (*agent.ModuleStatusList, error) {
	return mm.requestAgentAboutModules(dst, mIDs, agent.Message_UPDATE_CONFIG_MODULES)
}
