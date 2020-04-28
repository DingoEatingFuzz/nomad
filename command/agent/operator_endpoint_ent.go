// +build ent

package agent

import (
	"bytes"
	"io"
	"net/http"

	"github.com/hashicorp/nomad/nomad/structs"
)

func (s *HTTPServer) OperatorLicenseRequest(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	switch req.Method {
	case "GET":
		return s.operatorGetLicense(resp, req)
	case "PUT":
		return s.operatorPutLicense(resp, req)
	default:
		return nil, CodedError(405, ErrInvalidMethod)
	}
}

func (s *HTTPServer) operatorGetLicense(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	var args structs.LicenseGetRequest

	if s.parse(resp, req, &args.Region, &args.QueryOptions) {
		return nil, nil
	}

	var reply structs.LicenseGetResponse
	if err := s.agent.RPC("License.GetLicense", &args, &reply); err != nil {
		return nil, err
	}

	return reply, nil
}

func (s *HTTPServer) operatorPutLicense(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	var args structs.LicenseUpsertRequest

	s.parseWriteRequest(req, &args.WriteRequest)

	buf := bytes.NewBuffer(nil)
	if _, err := io.Copy(buf, req.Body); err != nil {
		return nil, err
	}
	args.License = &structs.StoredLicense{
		Signed: buf.String(),
	}

	var reply structs.GenericResponse
	if err := s.agent.RPC("License.UpsertLicense", &args, &reply); err != nil {
		return nil, err
	}
	return reply, nil
}
