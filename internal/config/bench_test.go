package config_test

import (
	"fmt"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner/benchgen"
)

func BenchmarkConfigParse(b *testing.B) {
	for _, shape := range benchgen.AllShapes {
		for _, jobs := range benchgen.JobCounts {
			src := benchgen.ConfigYAML(shape, jobs)
			b.Run(fmt.Sprintf("%s/jobs%d", shape, jobs), func(b *testing.B) {
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()
				for b.Loop() {
					if _, err := config.Parse(src); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkConfigEmit(b *testing.B) {
	for _, shape := range benchgen.AllShapes {
		for _, jobs := range benchgen.JobCounts {
			src := benchgen.ConfigYAML(shape, jobs)
			c, err := config.Parse(src)
			if err != nil {
				b.Fatal(err)
			}
			b.Run(fmt.Sprintf("%s/jobs%d", shape, jobs), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := c.Marshal(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
