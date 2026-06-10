package server

import (
	"context"
	"fmt"

	pb "trading-platform/libs/contracts/session"
	"trading-platform/services/session-manager/internal/manager"
)

// GrpcServer implements the SessionServiceServer gRPC interface.
type GrpcServer struct {
	pb.UnimplementedSessionServiceServer
	sessionManager *manager.SessionManager
}

// NewGrpcServer creates a new gRPC server instance.
func NewGrpcServer(sm *manager.SessionManager) *GrpcServer {
	return &GrpcServer{sessionManager: sm}
}

// GetSession is the gRPC handler for retrieving a session.
func (s *GrpcServer) GetSession(ctx context.Context, req *pb.GetSessionRequest) (*pb.GetSessionResponse, error) {
	sessionName := req.GetName()
	if sessionName == "" {
		return &pb.GetSessionResponse{Error: "session name is required"}, nil
	}

	managedSession, err := s.sessionManager.GetSession(sessionName)
	if err != nil {
		return &pb.GetSessionResponse{Error: err.Error()}, nil
	}

	managedSession.Mu.RLock()
	defer managedSession.Mu.RUnlock()

	if managedSession.Session == nil || !managedSession.Session.IsLoggedIn {
		return &pb.GetSessionResponse{Error: fmt.Sprintf("session '%s' is not yet authenticated", sessionName)}, nil
	}

	// Map the generic session struct to the gRPC response message.
	sessionDetails := managedSession.Session

	getString := func(key string) string {
		if val, ok := sessionDetails.BrokerSpecific[key]; ok {
			if strVal, ok := val.(string); ok {
				return strVal
			}
		}
		return ""
	}

	return &pb.GetSessionResponse{
		SessionToken: sessionDetails.AuthToken,
		UserId:       sessionDetails.UserID,
		Gcid:         getString("gcid"),
		SessionId:    getString("session_id"),
		IrisUrl:      getString("iris_url"),
		ApolloUrl:    getString("apollo_url"),
	}, nil
}
