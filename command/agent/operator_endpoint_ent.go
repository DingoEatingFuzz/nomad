// +build ent

package agent

import (
	"net/http"

	"github.com/hashicorp/nomad-licensing/license"
	"github.com/hashicorp/nomad/api"
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

	return api.LicenseReply{
		License: convertToAPILicense(reply.NomadLicense),
		QueryMeta: api.QueryMeta{
			LastIndex:   reply.QueryMeta.Index,
			LastContact: reply.QueryMeta.LastContact,
			KnownLeader: reply.QueryMeta.KnownLeader,
		},
	}, nil
}

func convertToAPILicense(l *license.License) *api.License {
	var modules []string
	for _, m := range l.Modules {
		modules = append(modules, m.String())
	}

	return &api.License{
		LicenseID:       l.LicenseID,
		CustomerID:      l.CustomerID,
		InstallationID:  l.InstallationID,
		IssueTime:       l.IssueTime,
		StartTime:       l.StartTime,
		ExpirationTime:  l.ExpirationTime,
		TerminationTime: l.TerminationTime,
		Product:         l.Product,
		Flags:           l.Flags,
		Modules:         modules,
		Features:        l.Features.StringList(),
	}
}

func (s *HTTPServer) operatorPutLicense(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	var args structs.LicenseUpsertRequest

	s.parseWriteRequest(req, &args.WriteRequest)

	var license string
	err := decodeBody(req, &license)
	if err != nil {
		return nil, err
	}

	args.License = &structs.StoredLicense{
		Signed: license,
	}

	var reply structs.GenericResponse
	if err := s.agent.RPC("License.UpsertLicense", &args, &reply); err != nil {
		return nil, err
	}
	return reply, nil
}
