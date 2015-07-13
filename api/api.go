// Copyright 2015 ThoughtWorks, Inc.

// This file is part of Gauge.

// Gauge is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Gauge is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with Gauge.  If not, see <http://www.gnu.org/licenses/>.

package api

import (
	"errors"
	"fmt"
	"github.com/getgauge/common"
	"github.com/getgauge/gauge/config"
	"github.com/getgauge/gauge/conn"
	"github.com/getgauge/gauge/gauge_messages"
	"github.com/getgauge/gauge/logger"
	"github.com/golang/protobuf/proto"
	"net"
	"path"
	"strconv"
	"sync"
	"github.com/getgauge/gauge/runner"
	"github.com/getgauge/gauge/logger/execLogger"
	"github.com/getgauge/gauge/api/infoGatherer"
	"github.com/getgauge/gauge/env"
	"github.com/getgauge/gauge/parser"
	"github.com/getgauge/gauge/refactor"
)

type interface APIStarter {
	StartAPI() *runner, error
}


func StartAPI() (*gaugeApiMessageHandler, error) {
	env.LoadEnv(*currentEnv, false)
	err, apiHandler := startAPIService(0)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Failed to connect to test runner: %s", err))
	}
	return apiHandler, nil
}

func startAPIService(port int) (error, *gaugeApiMessageHandler) {
	specInfoGatherer := new(infoGatherer.SpecInfoGatherer)
	apiHandler := &gaugeApiMessageHandler{specInfoGatherer: specInfoGatherer}
	gaugeConnectionHandler, err := conn.NewGaugeConnectionHandler(port, apiHandler)
	if err != nil {
		return err, nil
	}
	if port == 0 {
		if err := common.SetEnvVariable(common.ApiPortEnvVariableName, strconv.Itoa(gaugeConnectionHandler.ConnectionPortNumber())); err != nil {
			return errors.New(fmt.Sprintf("Failed to set Env variable %s. %s", common.ApiPortEnvVariableName, err.Error())), nil
		}
	}
	go gaugeConnectionHandler.HandleMultipleConnections()
	apiHandler.runner = specInfoGatherer.MakeListOfAvailableSteps(nil)
	return nil, apiHandler
}

func runAPIServiceIndefinitely(port int, wg *sync.WaitGroup) {
	wg.Add(1)
	_, apiHandler := startAPIService(port)
	apiHandler.runner.Kill(execLogger.Current())
}

type gaugeApiMessageHandler struct {
	specInfoGatherer *infoGatherer.SpecInfoGatherer
	runner           *runner.TestRunner
}

func (handler *gaugeApiMessageHandler) MessageBytesReceived(bytesRead []byte, connection net.Conn) {
	apiMessage := &gauge_messages.APIMessage{}
	var responseMessage *gauge_messages.APIMessage
	err := proto.Unmarshal(bytesRead, apiMessage)
	if err != nil {
		logger.ApiLog.Error("Failed to read API proto message: %s\n", err.Error())
		responseMessage = handler.getErrorMessage(err)
	} else {
		logger.ApiLog.Debug("Api Request Received: %s", apiMessage)
		messageType := apiMessage.GetMessageType()
		switch messageType {
		case gauge_messages.APIMessage_GetProjectRootRequest:
			responseMessage = handler.projectRootRequestResponse(apiMessage)
			break
		case gauge_messages.APIMessage_GetInstallationRootRequest:
			responseMessage = handler.installationRootRequestResponse(apiMessage)
			break
		case gauge_messages.APIMessage_GetAllStepsRequest:
			responseMessage = handler.getAllStepsRequestResponse(apiMessage)
			break
		case gauge_messages.APIMessage_GetAllSpecsRequest:
			responseMessage = handler.getAllSpecsRequestResponse(apiMessage)
			break
		case gauge_messages.APIMessage_GetStepValueRequest:
			responseMessage = handler.getStepValueRequestResponse(apiMessage)
			break
		case gauge_messages.APIMessage_GetLanguagePluginLibPathRequest:
			responseMessage = handler.getLanguagePluginLibPath(apiMessage)
			break
		case gauge_messages.APIMessage_GetAllConceptsRequest:
			responseMessage = handler.getAllConceptsRequestResponse(apiMessage)
			break
		case gauge_messages.APIMessage_PerformRefactoringRequest:
			responseMessage = handler.performRefactoring(apiMessage)
			break
		case gauge_messages.APIMessage_ExtractConceptRequest:
			responseMessage = handler.extractConcept(apiMessage)
			break
		case gauge_messages.APIMessage_FormatSpecsRequest:
			responseMessage = handler.formatSpecs(apiMessage)
			break
		default:
			responseMessage = handler.createUnsupportedApiMessageResponse(apiMessage)
		}
	}
	handler.sendMessage(responseMessage, connection)
}

func (handler *gaugeApiMessageHandler) sendMessage(message *gauge_messages.APIMessage, connection net.Conn) {
	logger.ApiLog.Debug("Sending API response: %s", message)
	dataBytes, err := proto.Marshal(message)
	if err != nil {
		logger.ApiLog.Error("Failed to respond to API request. Could not Marshal response %s\n", err.Error())
	}
	if err := conn.Write(connection, dataBytes); err != nil {
		logger.ApiLog.Error("Failed to respond to API request. Could not write response %s\n", err.Error())
	}
}

func (handler *gaugeApiMessageHandler) projectRootRequestResponse(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	projectRootResponse := &gauge_messages.GetProjectRootResponse{ProjectRoot: proto.String(config.ProjectRoot)}
	return &gauge_messages.APIMessage{MessageType: gauge_messages.APIMessage_GetProjectRootResponse.Enum(), MessageId: message.MessageId, ProjectRootResponse: projectRootResponse}
}

func (handler *gaugeApiMessageHandler) installationRootRequestResponse(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	root, err := common.GetInstallationPrefix()
	if err != nil {
		logger.ApiLog.Error("Failed to find installation root while responding to API request. %s\n", err.Error())
		root = ""
	}
	installationRootResponse := &gauge_messages.GetInstallationRootResponse{InstallationRoot: proto.String(root)}
	return &gauge_messages.APIMessage{MessageType: gauge_messages.APIMessage_GetInstallationRootResponse.Enum(), MessageId: message.MessageId, InstallationRootResponse: installationRootResponse}
}

func (handler *gaugeApiMessageHandler) getAllStepsRequestResponse(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	stepValues := handler.specInfoGatherer.GetAvailableSteps()
	stepValueResponses := make([]*gauge_messages.ProtoStepValue, 0)
	for _, stepValue := range stepValues {
		stepValueResponses = append(stepValueResponses, parser.ConvertToProtoStepValue(stepValue))
	}
	getAllStepsResponse := &gauge_messages.GetAllStepsResponse{AllSteps: stepValueResponses}
	return &gauge_messages.APIMessage{MessageType: gauge_messages.APIMessage_GetAllStepResponse.Enum(), MessageId: message.MessageId, AllStepsResponse: getAllStepsResponse}
}

func (handler *gaugeApiMessageHandler) getAllSpecsRequestResponse(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	getAllSpecsResponse := handler.createGetAllSpecsResponseMessageFor(handler.specInfoGatherer.AvailableSpecs)
	return &gauge_messages.APIMessage{MessageType: gauge_messages.APIMessage_GetAllSpecsResponse.Enum(), MessageId: message.MessageId, AllSpecsResponse: getAllSpecsResponse}
}

func (handler *gaugeApiMessageHandler) getStepValueRequestResponse(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	request := message.GetStepValueRequest()
	stepText := request.GetStepText()
	hasInlineTable := request.GetHasInlineTable()
	stepValue, err := parser.ExtractStepValueAndParams(stepText, hasInlineTable)

	if err != nil {
		return handler.getErrorResponse(message, err)
	}
	stepValueResponse := &gauge_messages.GetStepValueResponse{StepValue: parser.ConvertToProtoStepValue(stepValue)}
	return &gauge_messages.APIMessage{MessageType: gauge_messages.APIMessage_GetStepValueResponse.Enum(), MessageId: message.MessageId, StepValueResponse: stepValueResponse}

}

func (handler *gaugeApiMessageHandler) getAllConceptsRequestResponse(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	allConceptsResponse := handler.createGetAllConceptsResponseMessageFor(handler.specInfoGatherer.GetConceptInfos())
	return &gauge_messages.APIMessage{MessageType: gauge_messages.APIMessage_GetAllConceptsResponse.Enum(), MessageId: message.MessageId, AllConceptsResponse: allConceptsResponse}
}

func (handler *gaugeApiMessageHandler) getLanguagePluginLibPath(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	libPathRequest := message.GetLibPathRequest()
	language := libPathRequest.GetLanguage()
	languageInstallDir, err := common.GetPluginInstallDir(language, "")
	if err != nil {
		return handler.getErrorMessage(err)
	}
	runnerInfo, err := runner.GetRunnerInfo(language)
	if err != nil {
		return handler.getErrorMessage(err)
	}
	relativeLibPath := runnerInfo.Lib
	libPath := path.Join(languageInstallDir, relativeLibPath)
	response := &gauge_messages.GetLanguagePluginLibPathResponse{Path: proto.String(libPath)}
	return &gauge_messages.APIMessage{MessageType: gauge_messages.APIMessage_GetLanguagePluginLibPathResponse.Enum(), MessageId: message.MessageId, LibPathResponse: response}
}

func (handler *gaugeApiMessageHandler) getErrorResponse(message *gauge_messages.APIMessage, err error) *gauge_messages.APIMessage {
	errorResponse := &gauge_messages.ErrorResponse{Error: proto.String(err.Error())}
	return &gauge_messages.APIMessage{MessageType: gauge_messages.APIMessage_ErrorResponse.Enum(), MessageId: message.MessageId, Error: errorResponse}

}

func (handler *gaugeApiMessageHandler) getErrorMessage(err error) *gauge_messages.APIMessage {
	id := common.GetUniqueId()
	errorResponse := &gauge_messages.ErrorResponse{Error: proto.String(err.Error())}
	return &gauge_messages.APIMessage{MessageType: gauge_messages.APIMessage_ErrorResponse.Enum(), MessageId: &id, Error: errorResponse}
}

func (handler *gaugeApiMessageHandler) createGetAllSpecsResponseMessageFor(specs []*parser.Specification) *gauge_messages.GetAllSpecsResponse {
	protoSpecs := make([]*gauge_messages.ProtoSpec, 0)
	for _, spec := range specs {
		protoSpecs = append(protoSpecs, parser.ConvertToProtoSpec(spec))
	}
	return &gauge_messages.GetAllSpecsResponse{Specs: protoSpecs}
}

func (handler *gaugeApiMessageHandler) createGetAllConceptsResponseMessageFor(conceptInfos []*gauge_messages.ConceptInfo) *gauge_messages.GetAllConceptsResponse {
	return &gauge_messages.GetAllConceptsResponse{Concepts: conceptInfos}
}

func (handler *gaugeApiMessageHandler) performRefactoring(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	refactoringRequest := message.PerformRefactoringRequest
	refactoringResult := refactor.PerformRephraseRefactoring(refactoringRequest.GetOldStep(), refactoringRequest.GetNewStep())
	if refactoringResult.success {
		logger.ApiLog.Info("%s", refactoringResult.String())
	} else {
		logger.ApiLog.Error("Refactoring response from gauge. Errors : %s", refactoringResult.errors)
	}
	response := &gauge_messages.PerformRefactoringResponse{Success: proto.Bool(refactoringResult.success), Errors: refactoringResult.errors, FilesChanged: refactoringResult.allFilesChanges()}
	return &gauge_messages.APIMessage{MessageId: message.MessageId, MessageType: gauge_messages.APIMessage_PerformRefactoringResponse.Enum(), PerformRefactoringResponse: response}
}

func (handler *gaugeApiMessageHandler) extractConcept(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	request := message.GetExtractConceptRequest()
	success, err, filesChanged := extractConcept(request.GetConceptName(), request.GetSteps(), request.GetConceptFileName(), request.GetChangeAcrossProject(), request.GetSelectedTextInfo())
	response := &gauge_messages.ExtractConceptResponse{IsSuccess: proto.Bool(success), Error: proto.String(err.Error()), FilesChanged: filesChanged}
	return &gauge_messages.APIMessage{MessageId: message.MessageId, MessageType: gauge_messages.APIMessage_ExtractConceptResponse.Enum(), ExtractConceptResponse: response}
}

func (handler *gaugeApiMessageHandler) formatSpecs(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	request := message.GetFormatSpecsRequest()
	results := formatSpecFiles(request.GetSpecs()...)
	warnings := make([]string, 0)
	errors := make([]string, 0)
	for _, result := range results {
		if result.error != nil {
			errors = append(errors, result.error.Error())
		}
		if result.warnings != nil {
			warningTexts := make([]string, 0)
			for _, warning := range result.warnings {
				warningTexts = append(warningTexts, warning.String())
			}
			warnings = append(warnings, warningTexts...)
		}
	}
	formatResponse := &gauge_messages.FormatSpecsResponse{Errors: errors, Warnings: warnings}
	return &gauge_messages.APIMessage{MessageId: message.MessageId, MessageType: gauge_messages.APIMessage_FormatSpecsResponse.Enum(), FormatSpecsResponse: formatResponse}
}

func (handler *gaugeApiMessageHandler) createUnsupportedApiMessageResponse(message *gauge_messages.APIMessage) *gauge_messages.APIMessage {
	return &gauge_messages.APIMessage{MessageId: message.MessageId,
		MessageType:                   gauge_messages.APIMessage_UnsupportedApiMessageResponse.Enum(),
		UnsupportedApiMessageResponse: &gauge_messages.UnsupportedApiMessageResponse{}}
}