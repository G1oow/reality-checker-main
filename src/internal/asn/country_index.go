package asn

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"
)

// 国家归属数据源标识，供调用方决定日志与降级策略
const (
	CountrySourceRIPE = "ripe"
	CountrySourceMMDB = "mmdb"
	CountrySourceNone = "none"
)

// defaultCountryIndexTTL 国家前缀集合的默认缓存时长
const defaultCountryIndexTTL = 6 * time.Hour

// addrRange 表示一个网段的地址区间
type addrRange struct {
	start netip.Addr
	end   netip.Addr
}

// countryPrefixSet 单个国家的公告前缀集合，按起始地址排序并预计算区间最大结束地址，
// 使归属判定为 O(log n)，避免对上千条网段逐个线性比较。
type countryPrefixSet struct {
	ranges    []addrRange
	maxEnd    []netip.Addr
	fetchedAt time.Time
}

// CountryPrefixIndex 基于 RIPE Stat country-resource-list 构建国家前缀集合，
// 用于在不依赖本地 GeoIP mmdb 的前提下判定「某网段/IP 是否属于某国」。
// 结果按国家缓存，TTL 内不重复拉取；拉取失败时优先沿用过期的缓存。
type CountryPrefixIndex struct {
	client *Client
	ttl    time.Duration

	mu   sync.Mutex
	sets map[string]*countryPrefixSet
}

// NewCountryPrefixIndex 创建国家前缀索引；client 为空时索引不可用。
func NewCountryPrefixIndex(client *Client, ttl ...time.Duration) *CountryPrefixIndex {
	validity := defaultCountryIndexTTL
	if len(ttl) > 0 && ttl[0] > 0 {
		validity = ttl[0]
	}
	return &CountryPrefixIndex{
		client: client,
		ttl:    validity,
		sets:   make(map[string]*countryPrefixSet),
	}
}

// Match 判断 addr 是否落在 country 的公告前缀集合内。
func (c *CountryPrefixIndex) Match(ctx context.Context, country string, addr netip.Addr) (bool, error) {
	if c == nil || c.client == nil {
		return false, fmt.Errorf("RIPE 国家前缀索引未初始化")
	}
	code := NormalizeCountryCode(country)
	if code == "" {
		return false, fmt.Errorf("国家代码必须是两位 ISO alpha-2（如 DE）")
	}
	if !addr.IsValid() {
		return false, fmt.Errorf("无效 IP 地址")
	}
	set, err := c.ensure(ctx, code)
	if err != nil {
		return false, err
	}
	return set.contains(addr.Unmap()), nil
}

// ensure 返回缓存的集合，必要时向 RIPE 拉取。
func (c *CountryPrefixIndex) ensure(ctx context.Context, country string) (*countryPrefixSet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cached := c.sets[country]
	if cached != nil && time.Since(cached.fetchedAt) < c.ttl {
		return cached, nil
	}

	resources, err := c.client.FetchCountryResources(ctx, country)
	if err != nil {
		// 已过期的缓存仍优于丢失整轮过滤能力
		if cached != nil {
			return cached, nil
		}
		return nil, err
	}

	prefixes := make([]string, 0, len(resources.IPv4)+len(resources.IPv6))
	prefixes = append(prefixes, resources.IPv4...)
	prefixes = append(prefixes, resources.IPv6...)
	set := newCountryPrefixSet(prefixes)
	if len(set.ranges) == 0 {
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("%s 的国家前缀清单为空", country)
	}
	c.sets[country] = set
	return set, nil
}

// newCountryPrefixSet 归一化并排序前缀，预计算区间最大结束地址。
func newCountryPrefixSet(prefixes []string) *countryPrefixSet {
	ranges := make([]addrRange, 0, len(prefixes))
	for _, raw := range prefixes {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		masked := prefix.Masked()
		ranges = append(ranges, addrRange{start: masked.Addr().Unmap(), end: lastAddrOf(masked).Unmap()})
	}
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].start.Less(ranges[j].start)
	})

	maxEnd := make([]netip.Addr, len(ranges))
	var current netip.Addr
	for i, item := range ranges {
		if i == 0 || current.Less(item.end) {
			current = item.end
		}
		maxEnd[i] = current
	}
	return &countryPrefixSet{ranges: ranges, maxEnd: maxEnd, fetchedAt: time.Now()}
}

// contains 判定地址是否落在任一区间内。
func (s *countryPrefixSet) contains(addr netip.Addr) bool {
	if s == nil || len(s.ranges) == 0 || !addr.IsValid() {
		return false
	}
	idx := sort.Search(len(s.ranges), func(i int) bool {
		return s.ranges[i].start.Compare(addr) > 0
	}) - 1
	if idx < 0 {
		return false
	}
	return s.maxEnd[idx].Compare(addr) >= 0
}

// lastAddrOf 返回网段的最后一个地址。
func lastAddrOf(prefix netip.Prefix) netip.Addr {
	masked := prefix.Masked()
	addr := masked.Addr()
	hostBits := addr.BitLen() - masked.Bits()
	if hostBits <= 0 {
		return addr
	}

	if addr.Is4() {
		buf := addr.As4()
		value := binary.BigEndian.Uint32(buf[:])
		if hostBits >= 32 {
			value = ^uint32(0)
		} else {
			value |= ^uint32(0) >> uint(32-hostBits)
		}
		binary.BigEndian.PutUint32(buf[:], value)
		return netip.AddrFrom4(buf)
	}

	buf := addr.As16()
	high := binary.BigEndian.Uint64(buf[:8])
	low := binary.BigEndian.Uint64(buf[8:])
	switch {
	case hostBits >= 64 && hostBits-64 >= 64:
		high = ^uint64(0)
		low = ^uint64(0)
	case hostBits >= 64:
		low = ^uint64(0)
		high |= ^uint64(0) >> uint(64-(hostBits-64))
	default:
		low |= ^uint64(0) >> uint(64-hostBits)
	}
	binary.BigEndian.PutUint64(buf[:8], high)
	binary.BigEndian.PutUint64(buf[8:], low)
	return netip.AddrFrom16(buf)
}

// CountryResolver 统一的国家归属判定：优先 RIPE 国家前缀集合，失败时回退本地 mmdb。
type CountryResolver struct {
	index *CountryPrefixIndex
	mmdb  *geoip2.Reader
}

// NewCountryResolver 创建国家归属判定器；client 为空时只用 mmdb，mmdb 为空时只用 RIPE。
func NewCountryResolver(client *Client, mmdb *geoip2.Reader) *CountryResolver {
	resolver := &CountryResolver{mmdb: mmdb}
	if client != nil {
		resolver.index = NewCountryPrefixIndex(client)
	}
	return resolver
}

// MatchAddr 判断 addr 是否属于 country，返回实际使用的数据源（ripe / mmdb / none）。
// none 表示两条数据源均无结论（无网络、无本地库或地址无记录），调用方应保守处理。
func (r *CountryResolver) MatchAddr(ctx context.Context, country string, addr netip.Addr) (bool, string) {
	if r == nil || !addr.IsValid() {
		return false, CountrySourceNone
	}
	code := NormalizeCountryCode(country)
	if code == "" {
		return false, CountrySourceNone
	}

	if r.index != nil {
		if matched, err := r.index.Match(ctx, code, addr); err == nil {
			return matched, CountrySourceRIPE
		}
	}

	if r.mmdb != nil {
		if rec, err := r.mmdb.Country(net.IP(addr.Unmap().AsSlice())); err == nil && rec != nil {
			got := strings.ToUpper(strings.TrimSpace(rec.Country.IsoCode))
			if got != "" {
				return got == code, CountrySourceMMDB
			}
		}
	}

	return false, CountrySourceNone
}

// FilterPrefixes 按国家过滤网段，语义与 FilterByCountry 一致：只看网段网络地址的归属。
// 数据源无结论时保留该网段（不能仅凭缺失否认归属），返回值 source 为实际使用的数据源。
func (r *CountryResolver) FilterPrefixes(ctx context.Context, prefixes []string, country string) ([]string, string) {
	kept := make([]string, 0, len(prefixes))
	source := CountrySourceNone
	for _, raw := range prefixes {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		masked := prefix.Masked()
		matched, used := r.MatchAddr(ctx, country, masked.Addr())
		if used != CountrySourceNone {
			source = used
		}
		if used == CountrySourceNone || matched {
			kept = append(kept, masked.String())
		}
	}
	return kept, source
}
