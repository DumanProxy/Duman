package fakedata

import (
	"fmt"
	"math/rand"
	"strings"
)

// --- Word Pools ---

var firstNames = []string{
	"James", "Mary", "John", "Patricia", "Robert", "Jennifer", "Michael", "Linda",
	"William", "Elizabeth", "David", "Barbara", "Richard", "Susan", "Joseph", "Jessica",
	"Thomas", "Sarah", "Charles", "Karen", "Emma", "Olivia", "Ava", "Isabella",
	"Sophia", "Liam", "Noah", "Oliver", "Elijah", "Lucas", "Alexander", "Daniel",
	"Matthew", "Henry", "Sebastian", "Jack", "Aiden", "Owen", "Samuel", "Ryan",
}

var lastNames = []string{
	"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis",
	"Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson",
	"Thomas", "Taylor", "Moore", "Jackson", "Martin", "Lee", "Perez", "Thompson",
	"White", "Harris", "Sanchez", "Clark", "Ramirez", "Lewis", "Robinson",
	"Walker", "Young", "Allen", "King", "Wright", "Scott", "Torres", "Nguyen",
}

var companyNames = []string{
	"Acme Corp", "TechVista", "GlobalSync", "DataFlow Inc", "NexGen Solutions",
	"CloudPeak", "InnovateTech", "PrimeLogic", "QuantumLeap", "VeloCity",
	"BlueOcean", "SilverLake", "GreenField", "RedShift", "GoldStar",
	"IronClad", "SwiftCode", "BrightPath", "DeepRoot", "SkyBridge",
}

var streetNames = []string{
	"Main St", "Oak Ave", "Elm St", "Park Blvd", "Cedar Ln", "Maple Dr",
	"Pine St", "Washington Ave", "Lake Rd", "Hill St", "Forest Dr", "River Rd",
	"Sunset Blvd", "Highland Ave", "Valley Rd", "Spring St", "Willow Way",
}

var countries = []string{
	"United States", "United Kingdom", "Germany", "France", "Japan", "Canada",
	"Australia", "Brazil", "India", "South Korea", "Netherlands", "Sweden",
	"Turkey", "Spain", "Italy", "Mexico", "Poland", "Norway", "Denmark", "Finland",
}

var colorNames = []string{
	"Red", "Blue", "Green", "Black", "White", "Gray", "Navy", "Teal",
	"Coral", "Gold", "Silver", "Ivory", "Olive", "Maroon", "Crimson", "Azure",
}

var statusValues = []string{
	"active", "inactive", "pending", "completed", "cancelled", "archived",
	"draft", "published", "processing", "approved", "rejected", "suspended",
}

var eventTypes = []string{
	"page_view", "click", "scroll", "impression", "conversion_pixel",
	"form_submit", "search", "add_to_cart", "purchase", "signup",
}

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
}

var loremWords = []string{
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit",
	"sed", "do", "eiusmod", "tempor", "incididunt", "ut", "labore", "et", "dolore",
	"magna", "aliqua", "enim", "ad", "minim", "veniam", "quis", "nostrud",
	"exercitation", "ullamco", "laboris", "nisi", "aliquip", "ex", "ea", "commodo",
}

var titleWords = []string{
	"Advanced", "Modern", "Complete", "Essential", "Ultimate", "Professional",
	"Practical", "Introduction", "Guide", "Handbook", "Mastering", "Building",
	"Designing", "Understanding", "Exploring", "Optimizing", "Implementing",
	"Strategic", "Digital", "Global", "Dynamic", "Creative", "Innovative",
}

var categoryProducts = map[string][]string{
	"Electronics": {
		"Sony WH-1000XM5 Headphones", "Samsung 65\" OLED TV", "MacBook Pro 14\" M3",
		"Canon EOS R5 Camera", "Apple iPad Air 5", "Bose SoundLink Speaker",
		"LG 34\" Ultrawide Monitor", "Logitech MX Master 3S", "Google Pixel 8 Pro",
		"DJI Mavic Air 3", "Anker PowerBank 20000", "Razer BlackWidow V4",
		"Dell XPS 15 Laptop", "Garmin Fenix 7 Watch", "JBL Charge 5 Speaker",
		"Sennheiser HD 660S2", "Nintendo Switch OLED", "Xbox Series X Console",
		"Fujifilm X-T5 Camera", "AMD Ryzen 9 7950X",
	},
	"Clothing": {
		"Nike Air Max 90", "Levi's 501 Original Jeans", "Adidas Ultraboost 22",
		"Patagonia Down Sweater", "North Face Thermoball Jacket", "Ray-Ban Wayfarer",
		"Uniqlo Heattech Crew Neck", "Champion Reverse Weave Hoodie",
		"Columbia Rain Jacket", "New Balance 574 Classic",
		"Calvin Klein Boxer Brief 3-Pack", "Under Armour Tech Polo",
		"Timberland 6\" Premium Boot", "Carhartt WIP Chase Hoodie",
		"Zara Slim Fit Blazer", "H&M Linen Shirt", "GAP Logo Pullover",
		"Vans Old Skool Sneaker", "Doc Martens 1460 Boot", "Polo Ralph Lauren T-Shirt",
	},
	"Home & Garden": {
		"Dyson V15 Detect Vacuum", "iRobot Roomba j7+", "KitchenAid Stand Mixer",
		"Instant Pot Duo 7-in-1", "Philips Hue Starter Kit", "Nest Thermostat",
		"Weber Spirit Gas Grill", "IKEA Kallax Shelf Unit", "Casper Original Mattress",
		"Nespresso Vertuo Plus", "Breville Barista Express", "Ring Video Doorbell 4",
		"Shark Navigator Vacuum", "Cuisinart Food Processor", "Vitamix E310 Blender",
		"Le Creuset Dutch Oven", "Dyson Pure Cool Tower Fan", "Simplehuman Trash Can",
		"Crate & Barrel Couch", "West Elm Coffee Table",
	},
	"Books": {
		"Atomic Habits by James Clear", "Project Hail Mary by Andy Weir",
		"Sapiens by Yuval Noah Harari", "The Psychology of Money",
		"Dune by Frank Herbert", "Educated by Tara Westover",
		"Thinking, Fast and Slow", "The Midnight Library by Matt Haig",
		"Becoming by Michelle Obama", "The Alchemist by Paulo Coelho",
		"1984 by George Orwell", "To Kill a Mockingbird by Harper Lee",
		"The Great Gatsby by F. Scott Fitzgerald", "Brave New World by Aldous Huxley",
		"The Catcher in the Rye by J.D. Salinger", "Lord of the Rings by J.R.R. Tolkien",
		"Harry Potter Collection Box Set", "The Art of War by Sun Tzu",
		"Meditations by Marcus Aurelius", "The 48 Laws of Power by Robert Greene",
	},
	"Sports": {
		"Wilson Evolution Basketball", "Titleist Pro V1 Golf Balls (12-Pack)",
		"Yeti Rambler 36oz Bottle", "Garmin Edge 840 Bike Computer",
		"Peloton Bike+", "NordicTrack Commercial Treadmill",
		"TRX All-in-One Suspension Trainer", "Bowflex SelectTech Dumbbells",
		"Fitbit Charge 5", "Hydro Flask 32oz Wide Mouth",
		"Coleman Sundome Tent 4-Person", "RTIC Soft Cooler 30",
		"Osprey Atmos AG 65 Backpack", "Black Diamond Climbing Harness",
		"Trek Domane Road Bike", "Head Graphene Tennis Racket",
		"Callaway Rogue ST Driver", "Speedo Vanquisher Goggles",
		"Manduka PRO Yoga Mat", "Thule Roof Rack Cargo Box",
	},
	"Beauty": {
		"Dyson Airwrap Styler", "Olaplex No.3 Hair Perfector",
		"CeraVe Moisturizing Cream", "The Ordinary Niacinamide Serum",
		"Charlotte Tilbury Pillow Talk Lipstick", "Drunk Elephant Protini Cream",
		"Tatcha Dewy Skin Cream", "Paula's Choice BHA Exfoliant",
		"Rare Beauty Soft Pinch Blush", "MAC Ruby Woo Lipstick",
		"NARS Radiant Creamy Concealer", "Fenty Beauty Pro Filt'r Foundation",
		"La Mer Moisturizing Cream", "SK-II Facial Treatment Essence",
		"Clinique Moisture Surge", "Urban Decay All Nighter Setting Spray",
		"Benefit Precisely My Brow Pencil", "YSL Libre Eau de Parfum",
		"Tom Ford Black Orchid", "Jo Malone Wood Sage & Sea Salt",
	},
	"Toys": {
		"LEGO Star Wars Millennium Falcon", "Barbie Dreamhouse",
		"Hot Wheels Ultimate Garage", "Nintendo Switch Lite",
		"PlayStation 5 DualSense Controller", "Monopoly Classic Edition",
		"Nerf Elite 2.0 Blaster", "Play-Doh Mega Pack",
		"Fisher-Price Laugh & Learn Chair", "Melissa & Doug Wooden Blocks",
		"Magna-Tiles Clear Colors 100-Piece", "Crayola Inspiration Art Case",
		"Transformers Optimus Prime", "Paw Patrol Tower Playset",
		"Baby Yoda Plush Toy", "Rubik's Cube 3x3", "UNO Card Game",
		"Jenga Classic Game", "Risk Strategy Board Game", "Ticket to Ride Board Game",
	},
	"Grocery": {
		"Organic Valley Whole Milk", "KIND Nut Bars Variety Pack",
		"Blue Diamond Almonds 1lb", "Starbucks Pike Place K-Cups",
		"Nespresso OriginalLine Capsules", "Justin's Almond Butter",
		"RXBar Protein Bar 12-Pack", "Clif Bar Energy Bar Variety",
		"Liquid Death Mountain Water 12-Pack", "Oatly Oat Milk",
		"Bob's Red Mill Rolled Oats", "Nutella Hazelnut Spread",
		"Tillamook Sharp Cheddar", "Kerrygold Irish Butter",
		"Rao's Marinara Sauce", "Sir Kensington's Mayo",
		"Cholula Hot Sauce", "Maldon Sea Salt Flakes",
		"McCormick Grill Mates Set", "Ghirardelli Dark Chocolate Squares",
	},
	"Automotive": {
		"Michelin Defender LTX M/S Tires", "WeatherTech Floor Mats",
		"Chemical Guys Car Wash Kit", "NOCO Boost Plus Jump Starter",
		"Garmin DashCam 67W", "Thinkware U3000 Dash Cam",
		"Armor All Interior Cleaner", "Rain-X Windshield Treatment",
		"Bosch ICON Wiper Blades", "Meguiar's Ultimate Polish",
		"Anker Roav Bluetooth Car Kit", "FIXD OBD2 Scanner",
		"Blackvue DR900X Dashcam", "Yakima Roof Rack System",
		"Husky Liners Floor Mats", "K&N Cold Air Intake",
		"Optima RedTop Battery", "Pioneer AVH Touchscreen Radio",
		"LED Headlight Bulb Upgrade Kit", "Turtle Wax Ceramic Spray",
	},
	"Office": {
		"Herman Miller Aeron Chair", "Steelcase Leap V2",
		"Apple Magic Keyboard", "Logitech C920 HD Webcam",
		"Blue Yeti USB Microphone", "Elgato Key Light",
		"CalDigit TS4 Thunderbolt Hub", "Samsung T7 Portable SSD 2TB",
		"Brother HL-L2350DW Laser Printer", "Sharpie Fine Point Markers 24-Pack",
		"Moleskine Classic Notebook", "Post-it Super Sticky Notes",
		"Fellowes Laminator", "Epson EcoTank ET-2850 Printer",
		"APC UPS Battery Backup 1500VA", "Cable Management Kit",
		"Standing Desk Converter", "Monitor Arm Mount", "Desk Organizer Set",
		"Noise Cancelling Headset",
	},
}

var categoryPriceRanges = map[string][2]float64{
	"Electronics":   {29.99, 2499.99},
	"Clothing":      {12.99, 399.99},
	"Home & Garden": {19.99, 1299.99},
	"Books":         {8.99, 59.99},
	"Sports":        {14.99, 2999.99},
	"Beauty":        {9.99, 349.99},
	"Toys":          {7.99, 499.99},
	"Grocery":       {3.99, 49.99},
	"Automotive":    {9.99, 899.99},
	"Office":        {12.99, 1499.99},
}

var emailDomains = []string{
	"example.com", "test.org", "mail.dev", "company.io", "corp.net",
}

// --- Generator Functions ---

// GenSerial generates auto-increment values.
func GenSerial(rowIndex int) []byte {
	return []byte(fmt.Sprintf("%d", rowIndex+1))
}

// GenInt generates a random integer in [min, max].
func GenInt(rng *rand.Rand, min, max int) []byte {
	if max <= min {
		max = min + 100
	}
	return []byte(fmt.Sprintf("%d", min+rng.Intn(max-min+1)))
}

// GenBigInt generates a random int64.
func GenBigInt(rng *rand.Rand) []byte {
	return []byte(fmt.Sprintf("%d", rng.Int63n(1000000)+1))
}

// GenFloat generates a random float with 2 decimal places in [min, max].
func GenFloat(rng *rand.Rand, min, max float64) []byte {
	if max <= min {
		max = min + 100.0
	}
	v := min + rng.Float64()*(max-min)
	v = float64(int(v*100)) / 100
	return []byte(fmt.Sprintf("%.2f", v))
}

// GenBool generates a random boolean.
func GenBool(rng *rand.Rand) []byte {
	if rng.Intn(2) == 0 {
		return []byte("true")
	}
	return []byte("false")
}

// GenUUID generates a random UUID v4 string.
func GenUUID(rng *rand.Rand) []byte {
	var buf [16]byte
	for i := range buf {
		buf[i] = byte(rng.Intn(256))
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return []byte(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(buf[0])<<24|uint32(buf[1])<<16|uint32(buf[2])<<8|uint32(buf[3]),
		uint16(buf[4])<<8|uint16(buf[5]),
		uint16(buf[6])<<8|uint16(buf[7]),
		uint16(buf[8])<<8|uint16(buf[9]),
		uint64(buf[10])<<40|uint64(buf[11])<<32|uint64(buf[12])<<24|
			uint64(buf[13])<<16|uint64(buf[14])<<8|uint64(buf[15]),
	))
}

// GenTimestamp generates a random timestamp in 2024.
func GenTimestamp(rng *rand.Rand) []byte {
	month := rng.Intn(12) + 1
	day := rng.Intn(28) + 1
	hour := rng.Intn(24)
	minute := rng.Intn(60)
	second := rng.Intn(60)
	return []byte(fmt.Sprintf("2024-%02d-%02dT%02d:%02d:%02dZ", month, day, hour, minute, second))
}

// GenDate generates a random date in 2024.
func GenDate(rng *rand.Rand) []byte {
	month := rng.Intn(12) + 1
	day := rng.Intn(28) + 1
	return []byte(fmt.Sprintf("2024-%02d-%02d", month, day))
}

// GenName generates a full person name.
func GenName(rng *rand.Rand) []byte {
	first := firstNames[rng.Intn(len(firstNames))]
	last := lastNames[rng.Intn(len(lastNames))]
	return []byte(first + " " + last)
}

// GenFirstName generates a first name.
func GenFirstName(rng *rand.Rand) []byte {
	return []byte(firstNames[rng.Intn(len(firstNames))])
}

// GenLastName generates a last name.
func GenLastName(rng *rand.Rand) []byte {
	return []byte(lastNames[rng.Intn(len(lastNames))])
}

// GenEmail generates an email address.
func GenEmail(rng *rand.Rand, rowIndex int) []byte {
	first := strings.ToLower(firstNames[rng.Intn(len(firstNames))])
	last := strings.ToLower(lastNames[rng.Intn(len(lastNames))])
	domain := emailDomains[rng.Intn(len(emailDomains))]
	return []byte(fmt.Sprintf("%s.%s%d@%s", first, last, rowIndex, domain))
}

// GenUsername generates a username.
func GenUsername(rng *rand.Rand, rowIndex int) []byte {
	first := strings.ToLower(firstNames[rng.Intn(len(firstNames))])
	return []byte(fmt.Sprintf("%s%d", first, rng.Intn(9999)))
}

// GenPrice generates a monetary value.
func GenPrice(rng *rand.Rand, min, max float64) []byte {
	if min <= 0 {
		min = 1.99
	}
	if max <= min {
		max = 999.99
	}
	v := min + rng.Float64()*(max-min)
	v = float64(int(v*100)) / 100
	return []byte(fmt.Sprintf("%.2f", v))
}

// GenQuantity generates a small positive integer.
func GenQuantity(rng *rand.Rand) []byte {
	return []byte(fmt.Sprintf("%d", rng.Intn(100)+1))
}

// GenStatus generates a random status string.
func GenStatus(rng *rand.Rand, values []string) []byte {
	if len(values) == 0 {
		values = statusValues
	}
	return []byte(values[rng.Intn(len(values))])
}

// GenRating generates a 1-5 rating.
func GenRating(rng *rand.Rand) []byte {
	return []byte(fmt.Sprintf("%d", rng.Intn(5)+1))
}

// GenTitle generates a short title.
func GenTitle(rng *rand.Rand) []byte {
	n := 2 + rng.Intn(3)
	words := make([]string, n)
	for i := range words {
		words[i] = titleWords[rng.Intn(len(titleWords))]
	}
	return []byte(strings.Join(words, " "))
}

// GenDescription generates a paragraph of text.
func GenDescription(rng *rand.Rand) []byte {
	n := 10 + rng.Intn(20)
	words := make([]string, n)
	for i := range words {
		words[i] = loremWords[rng.Intn(len(loremWords))]
	}
	return []byte(strings.Join(words, " "))
}

// GenProductName generates a realistic product name from category pools.
func GenProductName(rng *rand.Rand, category string) []byte {
	if products, ok := categoryProducts[category]; ok {
		return []byte(products[rng.Intn(len(products))])
	}
	// Generic fallback
	adj := titleWords[rng.Intn(len(titleWords))]
	noun := []string{"Widget", "Device", "Tool", "Kit", "Set", "Pack", "Unit", "System"}
	return []byte(adj + " " + noun[rng.Intn(len(noun))])
}

// GenCompanyName generates a company name.
func GenCompanyName(rng *rand.Rand) []byte {
	return []byte(companyNames[rng.Intn(len(companyNames))])
}

// GenAddress generates a street address.
func GenAddress(rng *rand.Rand) []byte {
	num := rng.Intn(9999) + 1
	street := streetNames[rng.Intn(len(streetNames))]
	return []byte(fmt.Sprintf("%d %s", num, street))
}

// GenPhone generates a phone number.
func GenPhone(rng *rand.Rand) []byte {
	return []byte(fmt.Sprintf("+1-%03d-%03d-%04d",
		rng.Intn(900)+100, rng.Intn(900)+100, rng.Intn(10000)))
}

// GenCountry generates a country name.
func GenCountry(rng *rand.Rand) []byte {
	return []byte(countries[rng.Intn(len(countries))])
}

// GenColor generates a color name.
func GenColor(rng *rand.Rand) []byte {
	return []byte(colorNames[rng.Intn(len(colorNames))])
}

// GenSlug generates a URL-safe slug.
func GenSlug(rng *rand.Rand) []byte {
	n := 2 + rng.Intn(3)
	words := make([]string, n)
	for i := range words {
		words[i] = strings.ToLower(loremWords[rng.Intn(len(loremWords))])
	}
	return []byte(strings.Join(words, "-"))
}

// GenURL generates a page URL path.
func GenURL(rng *rand.Rand) []byte {
	pages := []string{"/", "/products", "/about", "/contact", "/cart",
		"/checkout", "/search", "/categories", "/orders", "/dashboard"}
	return []byte(pages[rng.Intn(len(pages))])
}

// GenIPAddress generates a random IP address.
func GenIPAddress(rng *rand.Rand) []byte {
	return []byte(fmt.Sprintf("%d.%d.%d.%d",
		rng.Intn(223)+1, rng.Intn(256), rng.Intn(256), rng.Intn(254)+1))
}

// GenUserAgent generates a browser user agent string.
func GenUserAgent(rng *rand.Rand) []byte {
	return []byte(userAgents[rng.Intn(len(userAgents))])
}

// GenEventType generates an analytics event type.
func GenEventType(rng *rand.Rand) []byte {
	return []byte(eventTypes[rng.Intn(len(eventTypes))])
}

// GenJSONMeta generates a simple JSON metadata object.
func GenJSONMeta(rng *rand.Rand) []byte {
	return []byte(fmt.Sprintf(`{"source":"web","version":"%d.%d.%d"}`,
		rng.Intn(5)+1, rng.Intn(10), rng.Intn(20)))
}

// GenText generates random text of given word count.
func GenText(rng *rand.Rand, minWords, maxWords int) []byte {
	if maxWords <= minWords {
		maxWords = minWords + 5
	}
	n := minWords + rng.Intn(maxWords-minWords+1)
	words := make([]string, n)
	for i := range words {
		words[i] = loremWords[rng.Intn(len(loremWords))]
	}
	return []byte(strings.Join(words, " "))
}

// GenForeignKey generates a valid FK reference value.
func GenForeignKey(rng *rand.Rand, maxID int) []byte {
	if maxID <= 0 {
		maxID = 1
	}
	return []byte(fmt.Sprintf("%d", rng.Intn(maxID)+1))
}
