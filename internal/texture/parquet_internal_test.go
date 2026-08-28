package texture

import "testing"

func TestParquetHerringboneStrips(t *testing.T) {
	_, rowUpper, _, _ := parquetPlankUV(0, 0.02)
	_, rowLower, _, fvLower := parquetPlankUV(0, 0.21)
	if int(rowUpper)&1 != 0 {
		t.Fatalf("upper strip should have even row, got %v", rowUpper)
	}
	if int(rowLower)&1 != 1 {
		t.Fatalf("lower strip should have odd row, got %v", rowLower)
	}
	if fvLower*parquetPlankWid < parquetPlankWid*0.1 {
		t.Fatalf("lower fv=%v, want continuous rv coordinate", fvLower)
	}
}
