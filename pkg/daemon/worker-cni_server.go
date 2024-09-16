package daemon

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/emicklei/go-restful/v3"
	"k8s.io/klog/v2"

	"github.com/fengjinlin/kube-ovn/pkg/cni/request"
)

type CniServer interface {
	Run(stopCh <-chan struct{})
}

type cniServer struct {
	config *Configuration

	podWorker PodWorker

	server *http.Server
}

func NewCniServer(config *Configuration,
	podWorker PodWorker) (CniServer, error) {

	server := &cniServer{
		config:    config,
		podWorker: podWorker,
	}

	wsContainer := restful.NewContainer()
	wsContainer.EnableContentEncoding(true)

	ws := new(restful.WebService)
	ws.Path("/api/v1").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	wsContainer.Add(ws)

	ws.Route(
		ws.POST("/add").
			To(server.handleAdd).
			Reads(request.CniRequest{}))
	ws.Route(
		ws.POST("/del").
			To(server.handleDel).
			Reads(request.CniRequest{}))

	filter := func(request *restful.Request, response *restful.Response, chain *restful.FilterChain) {
		var (
			getRequestURI = func(request *restful.Request) (uri string) {
				if request.Request.URL != nil {
					uri = request.Request.URL.RequestURI()
				}
				return
			}
			reqLogFormatFunc = func(request *restful.Request) string {
				return fmt.Sprintf("[%s] Incoming %s %s %s request", time.Now().Format(time.RFC3339), request.Request.Proto,
					request.Request.Method, getRequestURI(request))
			}
			respLogFormatFunc = func(response *restful.Response, request *restful.Request, reqTime float64) string {
				return fmt.Sprintf("[%s] Outgoing response %s %s with %d status code in %vms", time.Now().Format(time.RFC3339),
					request.Request.Method, getRequestURI(request), response.StatusCode(), reqTime)
			}
		)
		klog.Infof(reqLogFormatFunc(request))
		start := time.Now()
		chain.ProcessFilter(request, response)
		elapsed := float64((time.Since(start)) / time.Millisecond)
		//cniOperationHistogram.WithLabelValues(
		//	nodeName,
		//	getRequestURI(request),
		//	fmt.Sprintf("%d", response.StatusCode())).Observe(elapsed / 1000)
		klog.Infof(respLogFormatFunc(response, request, elapsed))
	}
	ws.Filter(filter)

	server.server = &http.Server{
		Handler:           wsContainer,
		ReadHeaderTimeout: time.Second * 3,
	}

	return server, nil
}

func (cs *cniServer) Run(_ <-chan struct{}) {
	var (
		addr = cs.config.BindSocket
	)
	listener, err := net.Listen("unix", addr)
	if err != nil {
		klog.Errorf("failed to bind socket to %s: %v", addr, err)
		return
	}
	defer func() {
		if err := os.Remove(addr); err != nil {
			klog.Error(err)
		}
	}()

	klog.Infof("start listen on %s", addr)
	err = cs.server.Serve(listener)
	klog.ErrorS(err, "failed to serve", "addr", addr)
}

func (cs *cniServer) handleAdd(req *restful.Request, resp *restful.Response) {
	r := request.CniRequest{}
	if err := req.ReadEntity(&r); err != nil {
		errMsg := fmt.Errorf("failed to parse add request: %v", err)
		klog.Error(errMsg)
		if err := resp.WriteHeaderAndEntity(http.StatusBadRequest, request.CniResponse{Err: errMsg.Error()}); err != nil {
			klog.Errorf("failed to write response, %v", err)
		}
		return
	}

	klog.Infof("add port request: %+v", r)

	status := http.StatusOK
	podResp, err := cs.podWorker.HandleCniAddPodPort(&r)
	if err != nil {
		klog.Errorf("failed to add pod port: %v", err)
		status = http.StatusInternalServerError
		podResp = &request.CniResponse{Err: err.Error()}
	}
	klog.V(3).Infof("add pod resp: %+v", podResp)
	if err = resp.WriteHeaderAndEntity(status, podResp); err != nil {
		klog.Errorf("failed to write response: %v", err)
	}
}

func (cs *cniServer) handleDel(req *restful.Request, resp *restful.Response) {
	r := request.CniRequest{}
	if err := req.ReadEntity(&r); err != nil {
		errMsg := fmt.Errorf("failed to parse del request: %v", err)
		klog.Error(errMsg)
		if err := resp.WriteHeaderAndEntity(http.StatusBadRequest, request.CniResponse{Err: errMsg.Error()}); err != nil {
			klog.Errorf("failed to write response, %v", err)
		}
		return
	}

	klog.Infof("del port request: %+v", r)

	status := http.StatusOK
	podResp, err := cs.podWorker.HandleCniDelPodPort(&r)
	if err != nil {
		klog.Errorf("failed to del pod port: %v", err)
		status = http.StatusInternalServerError
		podResp = &request.CniResponse{Err: err.Error()}
	}
	if podResp == nil {
		resp.WriteHeader(http.StatusNoContent)
	} else {
		if err = resp.WriteHeaderAndEntity(status, podResp); err != nil {
			klog.Errorf("failed to write response: %v", err)
		}
	}

}
