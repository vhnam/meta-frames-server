package roll

import "meta-frames-server/internal/domain"

// Lifecycle stages shown as the Rolls tabs. The first three are the raw statuses of the same name.
const (
	stageProcessing = "processing"
	stageCompleted  = "completed"
)

// Badges inside the processing stage.
const (
	detailLabDeveloping   = "lab_developing"
	detailLabScanning     = "lab_scanning"
	detailHomeDeveloping  = "home_developing"
	detailHomeScanning    = "home_scanning"
	detailRescanning      = "rescanning"
	detailWaitingScan     = "waiting_scan"
	detailWaitingHomeScan = "waiting_home_scan"
	detailWaitingLabScan  = "waiting_lab_scan"
	detailPrinting        = "printing"
)

const (
	actorSelf = "self"
	actorLab  = "lab"
)

// stageJob is what the stage rule reads from one live job.
// Field names match the stage_jobs JSON of ListRollSummaries.
type stageJob struct {
	Type          string `json:"type"`
	AtLab         bool   `json:"atLab"`
	Developed     bool   `json:"developed"`     // developed_at is set
	NegativesBack bool   `json:"negativesBack"` // negatives_returned_at is set
	ScansBack     bool   `json:"scansBack"`     // scans_received_at is set
}

// finished: the job's goal result is recorded. A develop job is finished once the film is
// developed, even while a lab still holds the negatives; that is what the server status rule
// does not see.
func (job stageJob) finished() bool {
	switch job.Type {
	case domain.JobTypeDevelop:
		return job.Developed || job.NegativesBack
	case domain.JobTypeDevelopScan, domain.JobTypeScan:
		return job.ScansBack
	default: // print
		return job.NegativesBack
	}
}

// unfinishedRank orders unfinished jobs for the badge: the earliest step wins.
var unfinishedRank = map[string]int{
	domain.JobTypeDevelop: 0, domain.JobTypeDevelopScan: 1, domain.JobTypeScan: 2, domain.JobTypePrint: 3,
}

// rollStage is the one rule behind the Rolls tabs, their counts and the badge. It reads the jobs,
// not the raw status, past done_shooting; the plan only decides which "waiting" badge shows.
func rollStage(status string, target, scanBy *string, jobs []stageJob) (string, *string) {
	switch status {
	case domain.RollStatusInStock, domain.RollStatusInCamera, domain.RollStatusDoneShooting:
		return status, nil
	}
	var current *stageJob
	developed, scanned := false, false
	for index := range jobs {
		job := &jobs[index]
		if job.ScansBack {
			scanned = true
		}
		if (job.Type == domain.JobTypeDevelop || job.Type == domain.JobTypeDevelopScan) && (job.Developed || job.NegativesBack || job.ScansBack) {
			developed = true
		}
		if !job.finished() && (current == nil || unfinishedRank[job.Type] < unfinishedRank[current.Type]) {
			current = job
		}
	}
	if current != nil {
		detail := unfinishedDetail(*current, scanned)
		return stageProcessing, &detail
	}
	if target != nil && *target == targetScanned && developed && !scanned {
		detail := detailWaitingScan
		switch {
		case scanBy != nil && *scanBy == actorSelf:
			detail = detailWaitingHomeScan
		case scanBy != nil && *scanBy == actorLab:
			detail = detailWaitingLabScan
		}
		return stageProcessing, &detail
	}
	return stageCompleted, nil
}

// unfinishedDetail is the badge for the job that holds the roll in processing. rollScanned says
// an earlier job already brought scans back, which makes an open scan job a rescan.
func unfinishedDetail(job stageJob, rollScanned bool) string {
	switch job.Type {
	case domain.JobTypeDevelop:
		return byPlace(job.AtLab, detailLabDeveloping, detailHomeDeveloping)
	case domain.JobTypeDevelopScan:
		// negatives back without developed_at: old jobs and mobile records never set it
		if job.Developed || job.NegativesBack {
			return byPlace(job.AtLab, detailLabScanning, detailHomeScanning)
		}
		return byPlace(job.AtLab, detailLabDeveloping, detailHomeDeveloping)
	case domain.JobTypeScan:
		if rollScanned {
			return detailRescanning
		}
		return byPlace(job.AtLab, detailLabScanning, detailHomeScanning)
	default:
		return detailPrinting
	}
}

func byPlace(atLab bool, lab, home string) string {
	if atLab {
		return lab
	}
	return home
}
