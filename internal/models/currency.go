package models

import (
	"sort"
	"strings"
	"unicode"
)

// Currency is one ISO 4217 entry shown in the picker.
type Currency struct {
	Code   string
	Name   string
	Symbol string
}

// Label is the compact "CODE — Name" string used in trigger displays.
func (c Currency) Label() string { return c.Code + " — " + c.Name }

// Currencies is the embedded ISO 4217 active currency list. Sorted at init
// time by normalized (accent-stripped, lowercased) name.
//
// We bundle all ~165 active currencies into the binary. The full list is a
// few KB and never needs to be fetched — this keeps the local-first
// invariant. Funds codes (CHE, CHW, BOV, MXV, etc.), precious metals
// (XAU/XAG/XPT/XPD), the testing code (XTS), and the no-currency code (XXX)
// are intentionally excluded.
var Currencies = []Currency{
	{"AFN", "Afghan Afghani", "؋"},
	{"ALL", "Albanian Lek", "L"},
	{"DZD", "Algerian Dinar", "DA"},
	{"AOA", "Angolan Kwanza", "Kz"},
	{"ARS", "Argentine Peso", "$"},
	{"AMD", "Armenian Dram", "֏"},
	{"AWG", "Aruban Florin", "ƒ"},
	{"AUD", "Australian Dollar", "A$"},
	{"AZN", "Azerbaijani Manat", "₼"},
	{"BSD", "Bahamian Dollar", "$"},
	{"BHD", "Bahraini Dinar", ".د.ب"},
	{"BDT", "Bangladeshi Taka", "৳"},
	{"BBD", "Barbadian Dollar", "$"},
	{"BYN", "Belarusian Ruble", "Br"},
	{"BZD", "Belize Dollar", "BZ$"},
	{"BMD", "Bermudian Dollar", "$"},
	{"BTN", "Bhutanese Ngultrum", "Nu."},
	{"BOB", "Bolivian Boliviano", "Bs."},
	{"BAM", "Bosnia-Herzegovina Convertible Mark", "KM"},
	{"BWP", "Botswanan Pula", "P"},
	{"BRL", "Brazilian Real", "R$"},
	{"GBP", "British Pound", "£"},
	{"BND", "Brunei Dollar", "B$"},
	{"BGN", "Bulgarian Lev", "лв"},
	{"BIF", "Burundian Franc", "FBu"},
	{"KHR", "Cambodian Riel", "៛"},
	{"CAD", "Canadian Dollar", "CA$"},
	{"CVE", "Cape Verdean Escudo", "$"},
	{"KYD", "Cayman Islands Dollar", "$"},
	{"XAF", "Central African CFA Franc", "FCFA"},
	{"XPF", "CFP Franc", "₣"},
	{"CLP", "Chilean Peso", "$"},
	{"CNY", "Chinese Yuan", "¥"},
	{"COP", "Colombian Peso", "$"},
	{"KMF", "Comorian Franc", "CF"},
	{"CDF", "Congolese Franc", "FC"},
	{"CRC", "Costa Rican Colón", "₡"},
	{"CUP", "Cuban Peso", "$"},
	{"CZK", "Czech Koruna", "Kč"},
	{"DKK", "Danish Krone", "kr"},
	{"DJF", "Djiboutian Franc", "Fdj"},
	{"DOP", "Dominican Peso", "RD$"},
	{"XCD", "East Caribbean Dollar", "EC$"},
	{"EGP", "Egyptian Pound", "E£"},
	{"ERN", "Eritrean Nakfa", "Nfk"},
	{"ETB", "Ethiopian Birr", "Br"},
	{"EUR", "Euro", "€"},
	{"FKP", "Falkland Islands Pound", "£"},
	{"FJD", "Fijian Dollar", "FJ$"},
	{"GMD", "Gambian Dalasi", "D"},
	{"GEL", "Georgian Lari", "₾"},
	{"GHS", "Ghanaian Cedi", "₵"},
	{"GIP", "Gibraltar Pound", "£"},
	{"GTQ", "Guatemalan Quetzal", "Q"},
	{"GNF", "Guinean Franc", "FG"},
	{"GYD", "Guyanaese Dollar", "G$"},
	{"HTG", "Haitian Gourde", "G"},
	{"HNL", "Honduran Lempira", "L"},
	{"HKD", "Hong Kong Dollar", "HK$"},
	{"HUF", "Hungarian Forint", "Ft"},
	{"ISK", "Icelandic Króna", "kr"},
	{"INR", "Indian Rupee", "₹"},
	{"IDR", "Indonesian Rupiah", "Rp"},
	{"IRR", "Iranian Rial", "﷼"},
	{"IQD", "Iraqi Dinar", "ع.د"},
	{"ILS", "Israeli New Shekel", "₪"},
	{"JMD", "Jamaican Dollar", "J$"},
	{"JPY", "Japanese Yen", "¥"},
	{"JOD", "Jordanian Dinar", "JD"},
	{"KZT", "Kazakhstani Tenge", "₸"},
	{"KES", "Kenyan Shilling", "KSh"},
	{"KWD", "Kuwaiti Dinar", "KD"},
	{"KGS", "Kyrgystani Som", "лв"},
	{"LAK", "Laotian Kip", "₭"},
	{"LBP", "Lebanese Pound", "L£"},
	{"LSL", "Lesotho Loti", "L"},
	{"LRD", "Liberian Dollar", "L$"},
	{"LYD", "Libyan Dinar", "LD"},
	{"MOP", "Macanese Pataca", "MOP$"},
	{"MKD", "Macedonian Denar", "ден"},
	{"MGA", "Malagasy Ariary", "Ar"},
	{"MWK", "Malawian Kwacha", "MK"},
	{"MYR", "Malaysian Ringgit", "RM"},
	{"MVR", "Maldivian Rufiyaa", "Rf"},
	{"MRU", "Mauritanian Ouguiya", "UM"},
	{"MUR", "Mauritian Rupee", "₨"},
	{"MXN", "Mexican Peso", "$"},
	{"MDL", "Moldovan Leu", "L"},
	{"MNT", "Mongolian Tögrög", "₮"},
	{"MAD", "Moroccan Dirham", "MAD"},
	{"MZN", "Mozambican Metical", "MT"},
	{"MMK", "Myanmar Kyat", "K"},
	{"NAD", "Namibian Dollar", "N$"},
	{"NPR", "Nepalese Rupee", "₨"},
	{"ANG", "Netherlands Antillean Guilder", "ƒ"},
	{"TWD", "New Taiwan Dollar", "NT$"},
	{"NZD", "New Zealand Dollar", "NZ$"},
	{"NIO", "Nicaraguan Córdoba", "C$"},
	{"NGN", "Nigerian Naira", "₦"},
	{"KPW", "North Korean Won", "₩"},
	{"NOK", "Norwegian Krone", "kr"},
	{"OMR", "Omani Rial", "﷼"},
	{"PKR", "Pakistani Rupee", "₨"},
	{"PAB", "Panamanian Balboa", "B/."},
	{"PGK", "Papua New Guinean Kina", "K"},
	{"PYG", "Paraguayan Guaraní", "₲"},
	{"PEN", "Peruvian Sol", "S/."},
	{"PHP", "Philippine Peso", "₱"},
	{"PLN", "Polish Złoty", "zł"},
	{"QAR", "Qatari Rial", "﷼"},
	{"RON", "Romanian Leu", "lei"},
	{"RUB", "Russian Ruble", "₽"},
	{"RWF", "Rwandan Franc", "RF"},
	{"SHP", "Saint Helena Pound", "£"},
	{"WST", "Samoan Tala", "T"},
	{"STN", "São Tomé & Príncipe Dobra", "Db"},
	{"SAR", "Saudi Riyal", "﷼"},
	{"RSD", "Serbian Dinar", "din"},
	{"SCR", "Seychellois Rupee", "₨"},
	{"SLE", "Sierra Leonean Leone", "Le"},
	{"SGD", "Singapore Dollar", "S$"},
	{"SBD", "Solomon Islands Dollar", "SI$"},
	{"SOS", "Somali Shilling", "S"},
	{"ZAR", "South African Rand", "R"},
	{"KRW", "South Korean Won", "₩"},
	{"SSP", "South Sudanese Pound", "SS£"},
	{"LKR", "Sri Lankan Rupee", "Rs"},
	{"SDG", "Sudanese Pound", "SDG"},
	{"SRD", "Surinamese Dollar", "$"},
	{"SZL", "Swazi Lilangeni", "L"},
	{"SEK", "Swedish Krona", "kr"},
	{"CHF", "Swiss Franc", "CHF"},
	{"SYP", "Syrian Pound", "£S"},
	{"TJS", "Tajikistani Somoni", "SM"},
	{"TZS", "Tanzanian Shilling", "TSh"},
	{"THB", "Thai Baht", "฿"},
	{"TOP", "Tongan Paʻanga", "T$"},
	{"TTD", "Trinidad & Tobago Dollar", "TT$"},
	{"TND", "Tunisian Dinar", "DT"},
	{"TRY", "Turkish Lira", "₺"},
	{"TMT", "Turkmenistani Manat", "T"},
	{"AED", "UAE Dirham", "د.إ"},
	{"UGX", "Ugandan Shilling", "USh"},
	{"UAH", "Ukrainian Hryvnia", "₴"},
	{"USD", "US Dollar", "$"},
	{"UYU", "Uruguayan Peso", "$U"},
	{"UZS", "Uzbekistan Som", "лв"},
	{"VUV", "Vanuatu Vatu", "VT"},
	{"VES", "Venezuelan Bolívar", "Bs."},
	{"VED", "Venezuelan Bolívar Soberano", "VED"},
	{"VND", "Vietnamese Đồng", "₫"},
	{"XOF", "West African CFA Franc", "CFA"},
	{"YER", "Yemeni Rial", "﷼"},
	{"ZMW", "Zambian Kwacha", "ZK"},
	{"ZWG", "Zimbabwean Gold", "ZiG"},
}

func init() {
	sort.SliceStable(Currencies, func(i, j int) bool {
		return foldKey(Currencies[i].Name) < foldKey(Currencies[j].Name)
	})
}

// LookupCurrency returns the Currency for code, or false. Comparison is
// case-insensitive on the code.
func LookupCurrency(code string) (Currency, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, c := range Currencies {
		if c.Code == code {
			return c, true
		}
	}
	return Currency{}, false
}

// SearchKey returns the lowercased, accent-stripped string used as the
// combobox `data-search` attribute. We do this server-side so the JS just
// runs `indexOf` against a pre-normalized blob.
func (c Currency) SearchKey() string {
	return foldKey(c.Code) + " " + foldKey(c.Name) + " " + foldKey(c.Symbol)
}

// foldKey lowercases s and folds common Latin accents to their ASCII base
// so "Cordoba" matches "Córdoba", etc. This keeps the picker friendly for
// English-keyboard users without bringing in golang.org/x/text.
func foldKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'á', 'à', 'â', 'ã', 'ä', 'å', 'ă', 'ā', 'Á', 'À', 'Â', 'Ã', 'Ä', 'Å':
			b.WriteRune('a')
		case 'é', 'è', 'ê', 'ë', 'ē', 'É', 'È', 'Ê', 'Ë':
			b.WriteRune('e')
		case 'í', 'ì', 'î', 'ï', 'ī', 'Í', 'Ì', 'Î', 'Ï':
			b.WriteRune('i')
		case 'ó', 'ò', 'ô', 'õ', 'ö', 'ø', 'ō', 'Ó', 'Ò', 'Ô', 'Õ', 'Ö', 'Ø':
			b.WriteRune('o')
		case 'ú', 'ù', 'û', 'ü', 'ū', 'Ú', 'Ù', 'Û', 'Ü':
			b.WriteRune('u')
		case 'ñ', 'Ñ':
			b.WriteRune('n')
		case 'ç', 'Ç':
			b.WriteRune('c')
		case 'ł', 'Ł':
			b.WriteRune('l')
		case 'đ', 'Đ', 'Ð':
			b.WriteRune('d')
		case 'ż', 'ź', 'Ż', 'Ź':
			b.WriteRune('z')
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
