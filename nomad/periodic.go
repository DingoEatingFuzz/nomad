package nomad

import (
	"container/heap"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/nomad/nomad/structs"
)

const (
	// The string appended to the periodic jobs ID when launching derived
	// instances of it.
	JobLaunchSuffix = "/periodic-"
)

// PeriodicDispatch is used to track and launch periodic jobs. It maintains the
// set of periodic jobs and creates derived jobs and evaluations per
// instantiation which is determined by the periodic spec.
type PeriodicDispatch struct {
	dispatcher JobEvalDispatcher
	enabled    bool
	running    bool

	tracked map[string]*structs.Job
	heap    *periodicHeap

	updateCh chan struct{}
	stopCh   chan struct{}
	waitCh   chan struct{}
	logger   *log.Logger
	l        sync.RWMutex
}

// JobEvalDispatcher is an interface to submit jobs and have evaluations created
// for them.
type JobEvalDispatcher interface {
	// DispatchJob takes a job a new, untracked job and creates an evaluation
	// for it.
	DispatchJob(job *structs.Job) error
}

// DispatchJob creates an evaluation for the passed job and commits both the
// evaluation and the job to the raft log.
func (s *Server) DispatchJob(job *structs.Job) error {
	// Commit this update via Raft
	req := structs.JobRegisterRequest{Job: job}
	_, index, err := s.raftApply(structs.JobRegisterRequestType, req)
	if err != nil {
		return err
	}

	// Create a new evaluation
	eval := &structs.Evaluation{
		ID:             structs.GenerateUUID(),
		Priority:       job.Priority,
		Type:           job.Type,
		TriggeredBy:    structs.EvalTriggerJobRegister,
		JobID:          job.ID,
		JobModifyIndex: index,
		Status:         structs.EvalStatusPending,
	}
	update := &structs.EvalUpdateRequest{
		Evals: []*structs.Evaluation{eval},
	}

	// Commit this evaluation via Raft
	// XXX: There is a risk of partial failure where the JobRegister succeeds
	// but that the EvalUpdate does not.
	_, _, err = s.raftApply(structs.EvalUpdateRequestType, update)
	if err != nil {
		return err
	}

	return nil
}

// NewPeriodicDispatch returns a periodic dispatcher that is used to track and
// launch periodic jobs.
func NewPeriodicDispatch(logger *log.Logger, dispatcher JobEvalDispatcher) *PeriodicDispatch {
	return &PeriodicDispatch{
		dispatcher: dispatcher,
		tracked:    make(map[string]*structs.Job),
		heap:       NewPeriodicHeap(),
		updateCh:   make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
		waitCh:     make(chan struct{}),
		logger:     logger,
	}
}

// SetEnabled is used to control if the periodic dispatcher is enabled. It
// should only be enabled on the active leader. Disabling an active dispatcher
// will stop any launched go routine and flush the dispatcher.
func (p *PeriodicDispatch) SetEnabled(enabled bool) {
	p.l.Lock()
	p.enabled = enabled
	p.l.Unlock()
	if !enabled {
		if p.running {
			close(p.stopCh)
			<-p.waitCh
			p.running = false
		}
		p.Flush()
	}
}

// Start begins the goroutine that creates derived jobs and evals.
func (p *PeriodicDispatch) Start() {
	p.l.Lock()
	p.running = true
	p.l.Unlock()
	go p.run()
}

// Tracked returns the set of tracked job IDs.
func (p *PeriodicDispatch) Tracked() []*structs.Job {
	p.l.RLock()
	defer p.l.RUnlock()
	tracked := make([]*structs.Job, len(p.tracked))
	i := 0
	for _, job := range p.tracked {
		tracked[i] = job
		i++
	}
	return tracked
}

// Add begins tracking of a periodic job. If it is already tracked, it acts as
// an update to the jobs periodic spec.
func (p *PeriodicDispatch) Add(job *structs.Job) error {
	p.l.Lock()
	defer p.l.Unlock()

	// Do nothing if not enabled
	if !p.enabled {
		return nil
	}

	// If we were tracking a job and it has been disabled or made non-periodic remove it.
	disabled := !job.IsPeriodic() || !job.Periodic.Enabled
	_, tracked := p.tracked[job.ID]
	if disabled {
		if tracked {
			p.removeLocked(job.ID)
		}

		// If the job is disabled and we aren't tracking it, do nothing.
		return nil
	}

	// Add or update the job.
	p.tracked[job.ID] = job
	next := job.Periodic.Next(time.Now())
	if tracked {
		if err := p.heap.Update(job, next); err != nil {
			return fmt.Errorf("failed to update job %v launch time: %v", job.ID, err)
		}
		p.logger.Printf("[DEBUG] nomad.periodic: updated periodic job %q", job.ID)
	} else {
		if err := p.heap.Push(job, next); err != nil {
			return fmt.Errorf("failed to add job %v", job.ID, err)
		}
		p.logger.Printf("[DEBUG] nomad.periodic: registered periodic job %q", job.ID)
	}

	// Signal an update.
	if p.running {
		select {
		case p.updateCh <- struct{}{}:
		default:
		}
	}

	return nil
}

// Remove stops tracking the passed job. If the job is not tracked, it is a
// no-op.
func (p *PeriodicDispatch) Remove(jobID string) error {
	p.l.Lock()
	defer p.l.Unlock()
	return p.removeLocked(jobID)
}

// Remove stops tracking the passed job. If the job is not tracked, it is a
// no-op. It assumes this is called while a lock is held.
func (p *PeriodicDispatch) removeLocked(jobID string) error {
	// Do nothing if not enabled
	if !p.enabled {
		return nil
	}

	if job, tracked := p.tracked[jobID]; tracked {
		delete(p.tracked, jobID)
		if err := p.heap.Remove(job); err != nil {
			return fmt.Errorf("failed to remove tracked job %v: %v", jobID, err)
		}
	}

	// Signal an update.
	if p.running {
		select {
		case p.updateCh <- struct{}{}:
		default:
		}
	}

	p.logger.Printf("[DEBUG] nomad.periodic: deregistered periodic job %q", jobID)
	return nil
}

// ForceRun causes the periodic job to be evaluated immediately.
func (p *PeriodicDispatch) ForceRun(jobID string) error {
	p.l.Lock()
	defer p.l.Unlock()

	// Do nothing if not enabled
	if !p.enabled {
		return fmt.Errorf("periodic dispatch disabled")
	}

	job, tracked := p.tracked[jobID]
	if !tracked {
		return fmt.Errorf("can't force run non-tracked job %v", jobID)
	}

	return p.createEval(job, time.Now())
}

// shouldRun returns whether the long lived run function should run.
func (p *PeriodicDispatch) shouldRun() bool {
	p.l.RLock()
	defer p.l.RUnlock()
	return p.enabled && p.running
}

// run is a long-lived function that waits till a job's periodic spec is met and
// then creates an evaluation to run the job.
func (p *PeriodicDispatch) run() {
	defer close(p.waitCh)
	var now time.Time
	for p.shouldRun() {
		job, launch, err := p.nextLaunch()
		if err != nil {
			p.l.RLock()
			defer p.l.RUnlock()
			if !p.running {
				p.logger.Printf("[ERR] nomad.periodic: failed to determine next periodic job: %v", err)
			}
			return
		} else if job == nil {
			return
		}

		now = time.Now()
		p.logger.Printf("[DEBUG] nomad.periodic: launching job %q in %s", job.ID, launch.Sub(now))

		select {
		case <-p.stopCh:
			return
		case <-p.updateCh:
			continue
		case <-time.After(launch.Sub(now)):
			// Get the current time so that we don't miss any jobs will we're creating evals.
			now = time.Now()
			p.dispatch(launch, now)
		}
	}
}

// dispatch scans the periodic jobs in order of launch time and creates
// evaluations for all jobs whose next launch time is equal to that of the
// passed launchTime. The now time is used to determine the next launch time for
// the dispatched jobs.
func (p *PeriodicDispatch) dispatch(launchTime time.Time, now time.Time) {
	p.l.Lock()
	defer p.l.Unlock()

	// Create evals for all the jobs with the same launch time.
	for {
		if p.heap.Length() == 0 {
			return
		}

		j, err := p.heap.Peek()
		if err != nil {
			p.logger.Printf("[ERR] nomad.periodic: failed to determine next periodic job: %v", err)
			return
		}

		if j.next != launchTime {
			return
		}

		if err := p.heap.Update(j.job, j.job.Periodic.Next(now)); err != nil {
			p.logger.Printf("[ERR] nomad.periodic: failed to update next launch of periodic job %q: %v", j.job.ID, err)
		}

		p.logger.Printf("[DEBUG] nomad.periodic: launching job %v at %v", j.job.ID, launchTime)
		go p.createEval(j.job, launchTime)
	}
}

// nextLaunch returns the next job to launch and when it should be launched. If
// the next job can't be determined, an error is returned. If the dispatcher is
// stopped, a nil job will be returned.
func (p *PeriodicDispatch) nextLaunch() (*structs.Job, time.Time, error) {
PICK:
	// If there is nothing wait for an update.
	p.l.RLock()
	if p.heap.Length() == 0 {
		p.l.RUnlock()

		// Block until there is an update, or the dispatcher is stopped.
		select {
		case <-p.stopCh:
			return nil, time.Time{}, nil
		case <-p.updateCh:
		}
		p.l.RLock()
	}

	nextJob, err := p.heap.Peek()
	p.l.RUnlock()
	if err != nil {
		select {
		case <-p.stopCh:
			return nil, time.Time{}, nil
		default:
			return nil, time.Time{}, err
		}
	}

	// If there are only invalid times, wait for an update.
	if nextJob.next.IsZero() {
		select {
		case <-p.stopCh:
			return nil, time.Time{}, nil
		case <-p.updateCh:
			goto PICK
		}
	}

	return nextJob.job, nextJob.next, nil
}

// createEval instantiates a job based on the passed periodic job and submits an
// evaluation for it.
func (p *PeriodicDispatch) createEval(periodicJob *structs.Job, time time.Time) error {
	derived, err := p.deriveJob(periodicJob, time)
	if err != nil {
		return err
	}

	if err := p.dispatcher.DispatchJob(derived); err != nil {
		p.logger.Printf("[ERR] nomad.periodic: failed to dispatch job %q: %v", periodicJob.ID, err)
		return err
	}

	return nil
}

// deriveJob instantiates a new job based on the passed periodic job and the
// launch time.
func (p *PeriodicDispatch) deriveJob(periodicJob *structs.Job, time time.Time) (
	derived *structs.Job, err error) {

	// Have to recover in case the job copy panics.
	defer func() {
		if r := recover(); r != nil {
			p.logger.Printf("[ERR] nomad.periodic: deriving job from"+
				" periodic job %v failed; deregistering from periodic runner: %v",
				periodicJob.ID, r)
			p.Remove(periodicJob.ID)
			derived = nil
			err = fmt.Errorf("Failed to create a copy of the periodic job %v: %v", periodicJob.ID, r)
		}
	}()

	// Create a copy of the periodic job, give it a derived ID/Name and make it
	// non-periodic.
	derived = periodicJob.Copy()
	derived.ParentID = periodicJob.ID
	derived.ID = p.derivedJobID(periodicJob, time)
	derived.Name = derived.ID
	derived.Periodic = nil
	derived.GC = true
	return
}

// deriveJobID returns a job ID based on the parent periodic job and the launch
// time.
func (p *PeriodicDispatch) derivedJobID(periodicJob *structs.Job, time time.Time) string {
	return fmt.Sprintf("%s%s%d", periodicJob.ID, JobLaunchSuffix, time.Unix())
}

// LaunchTime returns the launch time of the job. This is only valid for
// jobs created by PeriodicDispatch and will otherwise return an error.
func (p *PeriodicDispatch) LaunchTime(jobID string) (time.Time, error) {
	index := strings.LastIndex(jobID, JobLaunchSuffix)
	if index == -1 {
		return time.Time{}, fmt.Errorf("couldn't parse launch time from eval: %v", jobID)
	}

	launch, err := strconv.Atoi(jobID[index+len(JobLaunchSuffix):])
	if err != nil {
		return time.Time{}, fmt.Errorf("couldn't parse launch time from eval: %v", jobID)
	}

	return time.Unix(int64(launch), 0), nil
}

// Flush clears the state of the PeriodicDispatcher
func (p *PeriodicDispatch) Flush() {
	p.l.Lock()
	defer p.l.Unlock()
	p.stopCh = make(chan struct{})
	p.updateCh = make(chan struct{}, 1)
	p.waitCh = make(chan struct{})
	p.tracked = make(map[string]*structs.Job)
	p.heap = NewPeriodicHeap()
}

// periodicHeap wraps a heap and gives operations other than Push/Pop.
type periodicHeap struct {
	index map[string]*periodicJob
	heap  periodicHeapImp
}

type periodicJob struct {
	job   *structs.Job
	next  time.Time
	index int
}

func NewPeriodicHeap() *periodicHeap {
	return &periodicHeap{
		index: make(map[string]*periodicJob),
		heap:  make(periodicHeapImp, 0),
	}
}

func (p *periodicHeap) Push(job *structs.Job, next time.Time) error {
	if _, ok := p.index[job.ID]; ok {
		return fmt.Errorf("job %v already exists", job.ID)
	}

	pJob := &periodicJob{job, next, 0}
	p.index[job.ID] = pJob
	heap.Push(&p.heap, pJob)
	return nil
}

func (p *periodicHeap) Pop() (*periodicJob, error) {
	if len(p.heap) == 0 {
		return nil, errors.New("heap is empty")
	}

	pJob := heap.Pop(&p.heap).(*periodicJob)
	delete(p.index, pJob.job.ID)
	return pJob, nil
}

func (p *periodicHeap) Peek() (periodicJob, error) {
	if len(p.heap) == 0 {
		return periodicJob{}, errors.New("heap is empty")
	}

	return *(p.heap[0]), nil
}

func (p *periodicHeap) Contains(job *structs.Job) bool {
	_, ok := p.index[job.ID]
	return ok
}

func (p *periodicHeap) Update(job *structs.Job, next time.Time) error {
	if pJob, ok := p.index[job.ID]; ok {
		// Need to update the job as well because its spec can change.
		pJob.job = job
		pJob.next = next
		heap.Fix(&p.heap, pJob.index)
		return nil
	}

	return fmt.Errorf("heap doesn't contain job %v", job.ID)
}

func (p *periodicHeap) Remove(job *structs.Job) error {
	if pJob, ok := p.index[job.ID]; ok {
		heap.Remove(&p.heap, pJob.index)
		delete(p.index, job.ID)
		return nil
	}

	return fmt.Errorf("heap doesn't contain job %v", job.ID)
}

func (p *periodicHeap) Length() int {
	return len(p.heap)
}

type periodicHeapImp []*periodicJob

func (h periodicHeapImp) Len() int { return len(h) }

func (h periodicHeapImp) Less(i, j int) bool {
	// Two zero times should return false.
	// Otherwise, zero is "greater" than any other time.
	// (To sort it at the end of the list.)
	// Sort such that zero times are at the end of the list.
	iZero, jZero := h[i].next.IsZero(), h[j].next.IsZero()
	if iZero && jZero {
		return false
	} else if iZero {
		return false
	} else if jZero {
		return true
	}

	return h[i].next.Before(h[j].next)
}

func (h periodicHeapImp) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *periodicHeapImp) Push(x interface{}) {
	n := len(*h)
	job := x.(*periodicJob)
	job.index = n
	*h = append(*h, job)
}

func (h *periodicHeapImp) Pop() interface{} {
	old := *h
	n := len(old)
	job := old[n-1]
	job.index = -1 // for safety
	*h = old[0 : n-1]
	return job
}
