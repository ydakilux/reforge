package database

import (
	"context"
	"testing"
	"time"

	"github.com/ydakilux/reforge/internal/types"
)

// seedTestDB inserts known records covering all states: successes, errors,
// not_beneficial, already_hevc, across two drives with varying timestamps.
func seedTestDB(t *testing.T, store *SQLiteStore) {
	t.Helper()
	ctx := context.Background()

	now := time.Now().UTC()
	today := now.Format("2006-01-02T15:04:05Z")
	threeDaysAgo := now.Add(-3 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
	twoMonthsAgo := now.Add(-62 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")

	records := []struct {
		driveRoot string
		fileHash  string
		rec       types.Record
	}{
		// 5 successful conversions (no error, no special note)
		{`D:\`, "hash_s1", types.Record{
			OriginalSize: 10000, ConvertedSize: 6000, SourcePath: `D:\Videos\movie1.mkv`,
			Output: `D:\REFORGED\movie1.mp4`, SourceCodec: "h264", SourceContainer: "matroska",
			ConvertedAt: today,
		}},
		{`D:\`, "hash_s2", types.Record{
			OriginalSize: 20000, ConvertedSize: 12000, SourcePath: `D:\Videos\clip.avi`,
			Output: `D:\REFORGED\clip.mp4`, SourceCodec: "mpeg4", SourceContainer: "avi",
			ConvertedAt: today,
		}},
		{`E:\`, "hash_s3", types.Record{
			OriginalSize: 30000, ConvertedSize: 18000, SourcePath: `E:\Media\show.mkv`,
			Output: `E:\REFORGED\show.mp4`, SourceCodec: "h264", SourceContainer: "matroska",
			ConvertedAt: threeDaysAgo,
		}},
		{`D:\`, "hash_s4", types.Record{
			OriginalSize: 15000, ConvertedSize: 9000, SourcePath: `D:\Videos\tutorial.mp4`,
			Output: `D:\REFORGED\tutorial.mp4`, SourceCodec: "vp9", SourceContainer: "mp4",
			ConvertedAt: threeDaysAgo,
		}},
		{`E:\`, "hash_s5", types.Record{
			OriginalSize: 50000, ConvertedSize: 30000, SourcePath: `E:\Media\old.mkv`,
			Output: `E:\REFORGED\old.mp4`, SourceCodec: "h264", SourceContainer: "matroska",
			ConvertedAt: twoMonthsAgo,
		}},

		// 2 error records
		{`D:\`, "hash_e1", types.Record{
			OriginalSize: 8000, SourcePath: `D:\Videos\broken.mkv`,
			Error: "rc_1", SourceCodec: "h264", SourceContainer: "matroska",
			ConvertedAt: today,
		}},
		{`E:\`, "hash_e2", types.Record{
			OriginalSize: 12000, SourcePath: `E:\Media\Movies\corrupt.avi`,
			Error: "rc_139", SourceCodec: "mpeg4", SourceContainer: "avi",
			ConvertedAt: threeDaysAgo,
		}},

		// 2 not_beneficial records
		{`D:\`, "hash_nb1", types.Record{
			OriginalSize: 5000, ConvertedSize: 5500, SourcePath: `D:\Videos\small.mp4`,
			Note: "not_beneficial", SourceCodec: "h264", SourceContainer: "mp4",
			ConvertedAt: today,
		}},
		{`E:\`, "hash_nb2", types.Record{
			OriginalSize: 7000, ConvertedSize: 7200, SourcePath: `E:\Media\tiny.mkv`,
			Note: "not_beneficial", SourceCodec: "vp9", SourceContainer: "matroska",
			ConvertedAt: threeDaysAgo,
		}},

		// 2 already_hevc records
		{`D:\`, "hash_ah1", types.Record{
			OriginalSize: 9000, SourcePath: `D:\Videos\hevc1.mp4`,
			Note: "already_hevc", SourceCodec: "hevc", SourceContainer: "mp4",
			ConvertedAt: today,
		}},
		{`E:\`, "hash_ah2", types.Record{
			OriginalSize: 11000, SourcePath: `E:\Media\hevc2.mkv`,
			Note: "already_hevc", SourceCodec: "hevc", SourceContainer: "matroska",
			ConvertedAt: twoMonthsAgo,
		}},
	}

	for _, r := range records {
		if err := store.UpdateRecord(ctx, r.driveRoot, r.fileHash, r.rec); err != nil {
			t.Fatalf("seedTestDB: UpdateRecord(%s, %s): %v", r.driveRoot, r.fileHash, err)
		}
	}
}

func TestGetStats_AllDrives(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	st, err := store.GetStats(ctx, "")
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}

	if st.TotalFiles != 11 {
		t.Errorf("TotalFiles = %d, want 11", st.TotalFiles)
	}
	// original sizes: 10000+20000+30000+15000+50000+8000+12000+5000+7000+9000+11000 = 177000
	if st.TotalOriginal != 177000 {
		t.Errorf("TotalOriginal = %d, want 177000", st.TotalOriginal)
	}
	// converted sizes: 6000+12000+18000+9000+30000+0+0+5500+7200+0+0 = 87700
	if st.TotalConverted != 87700 {
		t.Errorf("TotalConverted = %d, want 87700", st.TotalConverted)
	}
	if st.ErrorCount != 2 {
		t.Errorf("ErrorCount = %d, want 2", st.ErrorCount)
	}
	if st.NotBeneficial != 2 {
		t.Errorf("NotBeneficial = %d, want 2", st.NotBeneficial)
	}
	if st.AlreadyHEVC != 2 {
		t.Errorf("AlreadyHEVC = %d, want 2", st.AlreadyHEVC)
	}
	// success = 11 - 2 - 2 - 2 = 5
	if st.SuccessCount != 5 {
		t.Errorf("SuccessCount = %d, want 5", st.SuccessCount)
	}
}

func TestGetStats_FilterByDrive(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	st, err := store.GetStats(ctx, `D:\`)
	if err != nil {
		t.Fatalf("GetStats(D:\\): %v", err)
	}

	// D:\ records: hash_s1, hash_s2, hash_s4, hash_e1, hash_nb1, hash_ah1 = 6
	if st.TotalFiles != 6 {
		t.Errorf("TotalFiles = %d, want 6", st.TotalFiles)
	}
	if st.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", st.ErrorCount)
	}
	if st.NotBeneficial != 1 {
		t.Errorf("NotBeneficial = %d, want 1", st.NotBeneficial)
	}
	if st.AlreadyHEVC != 1 {
		t.Errorf("AlreadyHEVC = %d, want 1", st.AlreadyHEVC)
	}
	// success = 6 - 1 - 1 - 1 = 3
	if st.SuccessCount != 3 {
		t.Errorf("SuccessCount = %d, want 3", st.SuccessCount)
	}
}

func TestGetStats_EmptyDB(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	st, err := store.GetStats(ctx, "")
	if err != nil {
		t.Fatalf("GetStats on empty DB: %v", err)
	}
	if st.TotalFiles != 0 {
		t.Errorf("TotalFiles = %d, want 0", st.TotalFiles)
	}
	if st.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", st.SuccessCount)
	}
}

func TestGetErrors_AllDrives(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	errs, err := store.GetErrors(ctx, "", "")
	if err != nil {
		t.Fatalf("GetErrors: %v", err)
	}
	if len(errs) != 2 {
		t.Fatalf("len(errors) = %d, want 2", len(errs))
	}

	errMap := make(map[string]ErrorRecord)
	for _, e := range errs {
		errMap[e.FileHash] = e
	}

	if e, ok := errMap["hash_e1"]; !ok {
		t.Error("missing hash_e1")
	} else {
		if e.Error != "rc_1" {
			t.Errorf("hash_e1 error = %q, want rc_1", e.Error)
		}
		if e.DriveRoot != `D:\` {
			t.Errorf("hash_e1 drive = %q, want D:\\", e.DriveRoot)
		}
	}
}

func TestGetErrors_FilterByDrive(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	errs, err := store.GetErrors(ctx, `E:\`, "")
	if err != nil {
		t.Fatalf("GetErrors(E:\\): %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("len(errors) = %d, want 1", len(errs))
	}
	if errs[0].FileHash != "hash_e2" {
		t.Errorf("FileHash = %q, want hash_e2", errs[0].FileHash)
	}
}

func TestGetErrors_PathFilter(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	errs, err := store.GetErrors(ctx, "", "%Movies%")
	if err != nil {
		t.Fatalf("GetErrors with pathFilter: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("len(errors) = %d, want 1", len(errs))
	}
	if errs[0].FileHash != "hash_e2" {
		t.Errorf("FileHash = %q, want hash_e2", errs[0].FileHash)
	}
}

func TestGetErrors_EmptyDB(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	errs, err := store.GetErrors(ctx, "", "")
	if err != nil {
		t.Fatalf("GetErrors on empty DB: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("len(errors) = %d, want 0", len(errs))
	}
}

func TestGetNotBeneficial_AllDrives(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	recs, err := store.GetNotBeneficial(ctx, "")
	if err != nil {
		t.Fatalf("GetNotBeneficial: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len = %d, want 2", len(recs))
	}

	nbMap := make(map[string]NotBeneficialRecord)
	for _, r := range recs {
		nbMap[r.FileHash] = r
	}

	if r, ok := nbMap["hash_nb1"]; !ok {
		t.Error("missing hash_nb1")
	} else {
		if r.OriginalSize != 5000 {
			t.Errorf("hash_nb1 OriginalSize = %d, want 5000", r.OriginalSize)
		}
		if r.ConvertedSize != 5500 {
			t.Errorf("hash_nb1 ConvertedSize = %d, want 5500", r.ConvertedSize)
		}
	}
}

func TestGetNotBeneficial_FilterByDrive(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	recs, err := store.GetNotBeneficial(ctx, `D:\`)
	if err != nil {
		t.Fatalf("GetNotBeneficial(D:\\): %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("len = %d, want 1", len(recs))
	}
	if recs[0].FileHash != "hash_nb1" {
		t.Errorf("FileHash = %q, want hash_nb1", recs[0].FileHash)
	}
}

func TestGetNotBeneficial_EmptyDB(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	recs, err := store.GetNotBeneficial(ctx, "")
	if err != nil {
		t.Fatalf("GetNotBeneficial on empty DB: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("len = %d, want 0", len(recs))
	}
}

func TestGetRecent(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	recs, err := store.GetRecent(ctx, 3)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("len = %d, want 3", len(recs))
	}

	// Most recent records should be from "today" timestamps
	for i := 0; i < len(recs)-1; i++ {
		if recs[i].ConvertedAt < recs[i+1].ConvertedAt {
			t.Errorf("records not in descending order: [%d]=%s > [%d]=%s",
				i, recs[i].ConvertedAt, i+1, recs[i+1].ConvertedAt)
		}
	}
}

func TestGetRecent_AllRecords(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	recs, err := store.GetRecent(ctx, 100)
	if err != nil {
		t.Fatalf("GetRecent(100): %v", err)
	}
	if len(recs) != 11 {
		t.Errorf("len = %d, want 11", len(recs))
	}
}

func TestGetRecent_EmptyDB(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	recs, err := store.GetRecent(ctx, 10)
	if err != nil {
		t.Fatalf("GetRecent on empty DB: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("len = %d, want 0", len(recs))
	}
}

func TestGetRecent_FieldPopulation(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	recs, err := store.GetRecent(ctx, 100)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}

	recMap := make(map[string]RecentRecord)
	for _, r := range recs {
		recMap[r.FileHash] = r
	}

	if r, ok := recMap["hash_s1"]; !ok {
		t.Error("missing hash_s1")
	} else {
		if r.SourceCodec != "h264" {
			t.Errorf("SourceCodec = %q, want h264", r.SourceCodec)
		}
		if r.OutputPath != `D:\REFORGED\movie1.mp4` {
			t.Errorf("OutputPath = %q, want D:\\REFORGED\\movie1.mp4", r.OutputPath)
		}
	}

	if r, ok := recMap["hash_e1"]; !ok {
		t.Error("missing hash_e1")
	} else {
		if r.Error != "rc_1" {
			t.Errorf("Error = %q, want rc_1", r.Error)
		}
	}
}

func TestGetFormatBreakdown_AllDrives(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	stats, err := store.GetFormatBreakdown(ctx, "")
	if err != nil {
		t.Fatalf("GetFormatBreakdown: %v", err)
	}

	// (h264, matroska): hash_s1, hash_s3, hash_s5, hash_e1 = 4
	// (mpeg4, avi): hash_s2, hash_e2 = 2
	// (vp9, mp4): hash_s4 = 1
	// (h264, mp4): hash_nb1 = 1
	// (vp9, matroska): hash_nb2 = 1
	// (hevc, mp4): hash_ah1 = 1
	// (hevc, matroska): hash_ah2 = 1

	fmtMap := make(map[string]FormatStat)
	for _, s := range stats {
		key := s.SourceCodec + "/" + s.SourceContainer
		fmtMap[key] = s
	}

	if s, ok := fmtMap["h264/matroska"]; !ok {
		t.Error("missing h264/matroska group")
	} else if s.Count != 4 {
		t.Errorf("h264/matroska count = %d, want 4", s.Count)
	}

	if s, ok := fmtMap["mpeg4/avi"]; !ok {
		t.Error("missing mpeg4/avi group")
	} else if s.Count != 2 {
		t.Errorf("mpeg4/avi count = %d, want 2", s.Count)
	}

	if s, ok := fmtMap["hevc/mp4"]; !ok {
		t.Error("missing hevc/mp4 group")
	} else if s.Count != 1 {
		t.Errorf("hevc/mp4 count = %d, want 1", s.Count)
	}
}

func TestGetFormatBreakdown_FilterByDrive(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	stats, err := store.GetFormatBreakdown(ctx, `E:\`)
	if err != nil {
		t.Fatalf("GetFormatBreakdown(E:\\): %v", err)
	}

	total := 0
	for _, s := range stats {
		total += s.Count
	}
	// E:\ records: hash_s3, hash_s5, hash_e2, hash_nb2, hash_ah2 = 5
	if total != 5 {
		t.Errorf("total records for E:\\ = %d, want 5", total)
	}
}

func TestGetFormatBreakdown_EmptyDB(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	stats, err := store.GetFormatBreakdown(ctx, "")
	if err != nil {
		t.Fatalf("GetFormatBreakdown on empty DB: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("len = %d, want 0", len(stats))
	}
}

func TestGetSpaceSaved_Total(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	res, err := store.GetSpaceSaved(ctx, "total")
	if err != nil {
		t.Fatalf("GetSpaceSaved(total): %v", err)
	}

	// Successful records (no error, no note): hash_s1..hash_s5
	// Saved: (10000-6000)+(20000-12000)+(30000-18000)+(15000-9000)+(50000-30000)
	//      = 4000 + 8000 + 12000 + 6000 + 20000 = 50000
	if res.FileCount != 5 {
		t.Errorf("FileCount = %d, want 5", res.FileCount)
	}
	if res.BytesSaved != 50000 {
		t.Errorf("BytesSaved = %d, want 50000", res.BytesSaved)
	}
	if res.Period != "total" {
		t.Errorf("Period = %q, want total", res.Period)
	}
}

func TestGetSpaceSaved_Week(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	res, err := store.GetSpaceSaved(ctx, "week")
	if err != nil {
		t.Fatalf("GetSpaceSaved(week): %v", err)
	}

	// Within last 7 days: today + 3 days ago
	// Successful in that range: hash_s1 (today), hash_s2 (today), hash_s3 (3d ago), hash_s4 (3d ago)
	// Saved: 4000 + 8000 + 12000 + 6000 = 30000
	if res.FileCount != 4 {
		t.Errorf("FileCount = %d, want 4", res.FileCount)
	}
	if res.BytesSaved != 30000 {
		t.Errorf("BytesSaved = %d, want 30000", res.BytesSaved)
	}
}

func TestGetSpaceSaved_Month(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	res, err := store.GetSpaceSaved(ctx, "month")
	if err != nil {
		t.Fatalf("GetSpaceSaved(month): %v", err)
	}

	// Within last 30 days: today + 3 days ago (not 2 months ago)
	// Same as week in this seed: hash_s1, hash_s2, hash_s3, hash_s4
	if res.FileCount != 4 {
		t.Errorf("FileCount = %d, want 4", res.FileCount)
	}
	if res.BytesSaved != 30000 {
		t.Errorf("BytesSaved = %d, want 30000", res.BytesSaved)
	}
}

func TestGetSpaceSaved_EmptyDB(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	res, err := store.GetSpaceSaved(ctx, "total")
	if err != nil {
		t.Fatalf("GetSpaceSaved on empty DB: %v", err)
	}
	if res.FileCount != 0 {
		t.Errorf("FileCount = %d, want 0", res.FileCount)
	}
	if res.BytesSaved != 0 {
		t.Errorf("BytesSaved = %d, want 0", res.BytesSaved)
	}
}

func TestGetSpaceSaved_InvalidPeriod(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	_, err := store.GetSpaceSaved(ctx, "invalid")
	if err == nil {
		t.Fatal("expected error for invalid period")
	}
}

func TestGetConversionTimeline_AllDrives(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	points, err := store.GetConversionTimeline(ctx)
	if err != nil {
		t.Fatalf("GetConversionTimeline: %v", err)
	}

	// Seed has 5 successful records across 3 dates and 2 drives.
	// today D:\: hash_s1 (10000/6000) + hash_s2 (20000/12000)
	// threeDaysAgo D:\: hash_s4 (15000/9000)
	// threeDaysAgo E:\: hash_s3 (30000/18000)
	// twoMonthsAgo E:\: hash_s5 (50000/30000)
	// Error/not_beneficial/already_hevc records are excluded.
	if len(points) == 0 {
		t.Fatal("expected timeline points, got 0")
	}

	totalCount := 0
	for _, p := range points {
		totalCount += p.Count
		if p.Date == "" {
			t.Error("timeline point has empty date")
		}
		if p.DriveRoot == "" {
			t.Error("timeline point has empty drive root")
		}
	}
	if totalCount != 5 {
		t.Errorf("total count across all points = %d, want 5", totalCount)
	}

	// Points must be in ascending date order.
	for i := 0; i < len(points)-1; i++ {
		if points[i].Date > points[i+1].Date {
			t.Errorf("points not in ascending order: [%d]=%s > [%d]=%s",
				i, points[i].Date, i+1, points[i+1].Date)
		}
	}
}

func TestGetConversionTimeline_EmptyDB(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	points, err := store.GetConversionTimeline(ctx)
	if err != nil {
		t.Fatalf("GetConversionTimeline on empty DB: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("len = %d, want 0", len(points))
	}
}

func TestGetDriveRoots(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	roots, err := store.GetDriveRoots(ctx)
	if err != nil {
		t.Fatalf("GetDriveRoots: %v", err)
	}

	if len(roots) != 2 {
		t.Fatalf("len = %d, want 2", len(roots))
	}

	// Should be sorted: D:\ before E:\
	if roots[0] != `D:\` {
		t.Errorf("roots[0] = %q, want D:\\", roots[0])
	}
	if roots[1] != `E:\` {
		t.Errorf("roots[1] = %q, want E:\\", roots[1])
	}
}

func TestGetDriveRoots_EmptyDB(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	roots, err := store.GetDriveRoots(ctx)
	if err != nil {
		t.Fatalf("GetDriveRoots on empty DB: %v", err)
	}
	if len(roots) != 0 {
		t.Errorf("len = %d, want 0", len(roots))
	}
}

func TestGetErrors_DriveAndPathFilter(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	// Both filters active but no match
	errs, err := store.GetErrors(ctx, `D:\`, "%Movies%")
	if err != nil {
		t.Fatalf("GetErrors with both filters: %v", err)
	}
	// D:\ error is hash_e1 at D:\Videos\broken.mkv — doesn't contain "Movies"
	if len(errs) != 0 {
		t.Errorf("len = %d, want 0 (D:\\ error has no 'Movies' in path)", len(errs))
	}
}

func TestGetFormatBreakdown_SizeAggregation(t *testing.T) {
	store := newTestSQLiteStore(t)
	seedTestDB(t, store)
	ctx := context.Background()

	stats, err := store.GetFormatBreakdown(ctx, "")
	if err != nil {
		t.Fatalf("GetFormatBreakdown: %v", err)
	}

	fmtMap := make(map[string]FormatStat)
	for _, s := range stats {
		key := s.SourceCodec + "/" + s.SourceContainer
		fmtMap[key] = s
	}

	// h264/matroska: hash_s1(10000/6000) + hash_s3(30000/18000) + hash_s5(50000/30000) + hash_e1(8000/0)
	// TotalOriginal = 98000, TotalConverted = 54000
	if s := fmtMap["h264/matroska"]; s.TotalOriginal != 98000 {
		t.Errorf("h264/matroska TotalOriginal = %d, want 98000", s.TotalOriginal)
	}
	if s := fmtMap["h264/matroska"]; s.TotalConverted != 54000 {
		t.Errorf("h264/matroska TotalConverted = %d, want 54000", s.TotalConverted)
	}
}

// Records just inside/outside the 7-day cutoff must be classified correctly
// even when they fall on the cutoff's calendar day (RFC3339 'T' vs SQLite ' ').
func TestGetSpaceSaved_WeekBoundaryHourPrecision(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	add := func(hash string, at time.Time) {
		t.Helper()
		if err := store.UpdateRecord(ctx, `D:\`, hash, types.Record{
			OriginalSize: 1000, ConvertedSize: 400, Output: `D:\REFORGED\x.mp4`,
			ConvertedAt: at.Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("UpdateRecord: %v", err)
		}
	}
	add("inside", now.Add(-7*24*time.Hour+2*time.Hour))
	add("outside", now.Add(-7*24*time.Hour-2*time.Hour))

	res, err := store.GetSpaceSaved(ctx, "week")
	if err != nil {
		t.Fatalf("GetSpaceSaved: %v", err)
	}
	if res.FileCount != 1 {
		t.Errorf("FileCount = %d, want 1 (only the record inside the 7-day window)", res.FileCount)
	}
}
