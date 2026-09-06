package datasets

import (
	"context"
	"strings"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type Province struct {
	Code           string `json:"code"`
	Classification string `json:"classification"`
	Name           string `json:"name"`
	EnglishName    string `json:"english_name"`
}

var provinces = []Province{
	{"110000", "cn-11", "北京市", "Beijing"}, {"120000", "cn-12", "天津市", "Tianjin"}, {"130000", "cn-13", "河北省", "Hebei"}, {"140000", "cn-14", "山西省", "Shanxi"}, {"150000", "cn-15", "内蒙古自治区", "Inner Mongolia"},
	{"210000", "cn-21", "辽宁省", "Liaoning"}, {"220000", "cn-22", "吉林省", "Jilin"}, {"230000", "cn-23", "黑龙江省", "Heilongjiang"},
	{"310000", "cn-31", "上海市", "Shanghai"}, {"320000", "cn-32", "江苏省", "Jiangsu"}, {"330000", "cn-33", "浙江省", "Zhejiang"}, {"340000", "cn-34", "安徽省", "Anhui"}, {"350000", "cn-35", "福建省", "Fujian"}, {"360000", "cn-36", "江西省", "Jiangxi"}, {"370000", "cn-37", "山东省", "Shandong"},
	{"410000", "cn-41", "河南省", "Henan"}, {"420000", "cn-42", "湖北省", "Hubei"}, {"430000", "cn-43", "湖南省", "Hunan"}, {"440000", "cn-44", "广东省", "Guangdong"}, {"450000", "cn-45", "广西壮族自治区", "Guangxi"}, {"460000", "cn-46", "海南省", "Hainan"},
	{"500000", "cn-50", "重庆市", "Chongqing"}, {"510000", "cn-51", "四川省", "Sichuan"}, {"520000", "cn-52", "贵州省", "Guizhou"}, {"530000", "cn-53", "云南省", "Yunnan"}, {"540000", "cn-54", "西藏自治区", "Tibet"},
	{"610000", "cn-61", "陕西省", "Shaanxi"}, {"620000", "cn-62", "甘肃省", "Gansu"}, {"630000", "cn-63", "青海省", "Qinghai"}, {"640000", "cn-64", "宁夏回族自治区", "Ningxia"}, {"650000", "cn-65", "新疆维吾尔自治区", "Xinjiang"},
}

func Provinces() []Province { return append([]Province(nil), provinces...) }

// parseMMDB retains only CN country and mainland province networks. It does
// not materialize global city maps or infer complete coverage from file family.
// CN records lacking a recognized subdivision remain outside region coverage.
func parseMMDB(ctx context.Context, data []byte, limits Limits) ([]groupWire, int, error) {
	if err := checkContext(ctx); err != nil {
		return nil, 0, err
	}
	reader, err := maxminddb.OpenBytes(data, maxminddb.DisableStringCache())
	if err != nil {
		return nil, 0, invalid("MMDB open: %v", err)
	}
	defer reader.Close()
	if reader.Metadata.NodeCount > uint(limits.MaxScanRecords) {
		return nil, 0, exhausted("MMDB search nodes")
	}
	separator := uint64(reader.Metadata.NodeCount) * uint64(reader.Metadata.RecordSize) / 4
	if separator+16 > uint64(len(data)) {
		return nil, 0, invalid("MMDB data separator")
	}
	for _, value := range data[separator : separator+16] {
		if value != 0 {
			return nil, 0, invalid("MMDB data separator")
		}
	}
	// A bounded bitmap avoids the global map used by Reader.Verify. Every
	// distinct referenced record is validated through a cancellation-aware,
	// allocation-bounded cursor walk, including records outside CN.
	verified := make([]byte, (len(data)+7)/8)
	groups := []groupWire{{Name: "cn", Kind: sdk.DatasetClassificationCountry, DisplayName: "中国"}}
	names := make(map[string]int)
	for _, province := range provinces {
		groups = append(groups, groupWire{Name: province.Classification, Kind: sdk.DatasetClassificationRegion, DisplayName: province.Name})
		position := len(groups) - 1
		for _, name := range []string{province.Code, province.Code[:2], province.Classification, province.Name, province.EnglishName} {
			names[strings.ToLower(name)] = position
		}
	}
	names["xizang"] = names["tibet"]
	names["nei mongol"] = names["inner mongolia"]
	scanned, retained := 0, 0
	for result := range reader.Networks() {
		scanned++
		if scanned > limits.MaxScanRecords {
			return nil, scanned, exhausted("MMDB scanned networks")
		}
		if scanned%1024 == 0 {
			if err := checkContext(ctx); err != nil {
				return nil, scanned, err
			}
		}
		if err := result.Err(); err != nil {
			return nil, scanned, invalid("MMDB network: %v", err)
		}
		offset := result.Offset()
		if offset >= uintptr(len(data)) {
			return nil, scanned, invalid("MMDB record offset")
		}
		if verified[offset/8]&(1<<uint(offset%8)) == 0 {
			verifier := mmdbRecordVerifier{ctx: ctx, remaining: 8192}
			if err := result.Decode(&verifier); err != nil {
				return nil, scanned, invalid("MMDB record validation: %v", err)
			}
			verified[offset/8] |= 1 << uint(offset%8)
		}
		var country string
		if err := result.DecodePath(&country, "country", "iso_code"); err != nil {
			return nil, scanned, invalid("MMDB country: %v", err)
		}
		if country != "CN" {
			continue
		}
		prefix := result.Prefix()
		if !prefix.IsValid() || prefix.Addr().Is4In6() {
			return nil, scanned, invalid("MMDB prefix")
		}
		groups[0].Prefixes = append(groups[0].Prefixes, prefix.String())
		retained++
		var record struct {
			Subdivisions []struct {
				Code  string `maxminddb:"iso_code"`
				Names struct {
					English string `maxminddb:"en"`
					Chinese string `maxminddb:"zh-CN"`
				} `maxminddb:"names"`
			} `maxminddb:"subdivisions"`
		}
		if err := result.Decode(&record); err != nil {
			return nil, scanned, invalid("MMDB subdivision: %v", err)
		}
		if len(record.Subdivisions) > 32 {
			return nil, scanned, exhausted("MMDB subdivisions")
		}
		if len(record.Subdivisions) > 0 {
			region := record.Subdivisions[0]
			position := 0
			for _, name := range []string{region.Code, region.Names.English, region.Names.Chinese} {
				if at := names[strings.ToLower(name)]; at > 0 {
					position = at
					break
				}
			}
			if position > 0 {
				groups[position].Prefixes = append(groups[position].Prefixes, prefix.String())
				retained++
			}
		}
		if retained > limits.MaxEntries || int64(retained)*192 > limits.MaxMemoryBytes {
			return nil, scanned, exhausted("MMDB retained CN index")
		}
	}
	for _, group := range groups {
		if len(group.Prefixes) == 0 {
			return nil, scanned, invalid("MMDB candidate lacks required CN classification %s", group.Name)
		}
	}
	return groups, scanned, nil
}
