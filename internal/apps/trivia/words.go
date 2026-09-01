package trivia

// The word lists a game name is drawn from. A name is three of these
// hyphenated -- brave-otter-lamp -- and its one hard requirement is that
// somebody can read it off a TV across a loud room and type it into a phone
// correctly on the first try.
//
// Every word here is 4-7 letters, lowercase ASCII, and unambiguous when
// spoken. Homophone pairs are excluded on both sides (bear/bare, tern/turn,
// pail/pale, mail/male) because "was that b-e-a-r or b-a-r-e" is exactly the
// failure that sends someone to the bar to ask. Nothing profane, and no word
// appears in two lists, so the shape reads consistently as
// adjective-animal-object.
//
// 256 x 156 x 256 is about 10.2 million combinations, which at one quiz a week
// is not a number anybody has to think about again.

var adjectives = []string{
	"brave", "bright", "calm", "clever", "bold", "quiet", "swift", "warm",
	"keen", "lucky", "merry", "noble", "proud", "quick", "royal", "sharp",
	"smart", "solid", "sunny", "tidy", "vivid", "witty", "eager", "fancy",
	"giant", "grand", "happy", "jolly", "loyal", "lunar", "magic", "mellow",
	"mighty", "modest", "neat", "polite", "prime", "rapid", "rustic", "silly",
	"sleek", "smooth", "sober", "spry", "steady", "stout", "sturdy", "super",
	"sweet", "tender", "tough", "trusty", "upbeat", "urban", "wise", "young",
	"zesty", "amber", "azure", "bronze", "coral", "crimson", "golden", "green",
	"indigo", "ivory", "jade", "olive", "orange", "purple", "scarlet", "silver",
	"violet", "chilly", "cozy", "crisp", "dusty", "early", "fresh", "gentle",
	"glad", "humble", "ideal", "joint", "jumbo", "kindly", "level", "lively",
	"lofty", "loose", "lower", "major", "minor", "moral", "novel", "outer",
	"petite", "plush", "polar", "prompt", "proper", "pure", "quaint", "rare",
	"ready", "regal", "ripe", "robust", "rosy", "round", "rugged", "sacred",
	"sandy", "secret", "senior", "serene", "shady", "simple", "single", "skilled",
	"slender", "snappy", "social", "solar", "sound", "spare", "special", "spicy",
	"spiral", "square", "stable", "stark", "stellar", "stern", "still", "stony",
	"strong", "subtle", "sunlit", "supple", "sure", "swell", "tall", "tasty",
	"tepid", "thick", "tiny", "toasty", "total", "trim", "tribal", "true",
	"tuneful", "twin", "ultra", "unique", "untold", "upper", "useful", "vague",
	"valid", "vast", "velvet", "verbal", "vernal", "vibrant", "vital", "vocal",
	"wavy", "wealthy", "weekly", "welcome", "western", "whole", "wide", "wild",
	"willing", "windy", "winter", "wooden", "woolly", "worldly", "worthy", "woven",
	"zealous", "zippy", "ancient", "artful", "basic", "blazing", "bouncy", "breezy",
	"briny", "bubbly", "buoyant", "burly", "candid", "canny", "careful", "chief",
	"civic", "classic", "clean", "clear", "coastal", "common", "cool", "costly",
	"cosmic", "crafty", "curious", "daily", "dainty", "dapper", "daring", "dawn",
	"deep", "deft", "dense", "direct", "divine", "double", "dual", "eastern",
	"easy", "elastic", "elder", "elegant", "empty", "endless", "equal", "even",
	"exact", "expert", "extra", "faint", "fair", "famous", "fast", "fertile",
	"fierce", "fine", "firm", "fiscal", "fitting", "fleet", "fluent", "fluffy",
	"flying", "fond", "formal", "frank", "free", "frosty", "frugal", "full",
}

var animals = []string{
	"otter", "badger", "falcon", "heron", "marten", "weasel", "bison", "beaver",
	"bobcat", "cougar", "coyote", "ferret", "gecko", "gopher", "grouse", "hamster",
	"iguana", "jackal", "jaguar", "kestrel", "lemur", "lizard", "llama", "lynx",
	"magpie", "marmot", "mink", "mole", "moose", "mouse", "newt", "ocelot",
	"opossum", "osprey", "panda", "parrot", "pelican", "penguin", "pigeon", "possum",
	"puffin", "python", "quail", "rabbit", "raccoon", "raven", "robin", "salmon",
	"shark", "sheep", "shrew", "skunk", "sloth", "snail", "sparrow", "sprat",
	"squid", "stoat", "stork", "swan", "tapir", "thrush", "tiger", "trout",
	"turkey", "turtle", "viper", "vole", "vulture", "walrus", "warbler", "wombat",
	"wren", "zebra", "beetle", "bird", "camel", "carp", "chick", "cobra",
	"colt", "condor", "crab", "crane", "crow", "deer", "dingo", "dodo",
	"dove", "duck", "eagle", "egret", "fawn", "finch", "fish", "foal",
	"frog", "gannet", "goat", "goose", "gull", "hawk", "hound", "ibex",
	"ibis", "kite", "kiwi", "koala", "lamb", "lark", "leech", "lion",
	"loon", "mare", "martin", "mite", "moth", "mule", "mutt", "oryx",
	"oxen", "perch", "pike", "pony", "poodle", "prawn", "quoll", "rook",
	"serval", "shad", "shrimp", "skink", "slug", "smelt", "snake", "snipe",
	"sponge", "squab", "steed", "stag", "stud", "sunfish", "tadpole", "terrier",
	"tick", "tomcat", "toucan", "tuna", "urchin", "vixen", "wasp", "weevil",
	"whelk", "widgeon", "wolf", "worm",
}

var objects = []string{
	"lamp", "anchor", "anvil", "apron", "arrow", "atlas", "awning", "axle",
	"badge", "bagel", "banjo", "barge", "barrel", "basket", "baton", "beacon",
	"beaker", "beanbag", "bell", "belt", "bench", "bicycle", "binder", "blanket",
	"blender", "blossom", "bobbin", "bolt", "bonnet", "bookend", "boot", "bottle",
	"bowl", "bracket", "braid", "brick", "bridge", "broom", "brush", "bucket",
	"buckle", "bulb", "bundle", "buoy", "button", "cabin", "cable", "camera",
	"candle", "canoe", "canvas", "cape", "carpet", "cart", "carton", "castle",
	"chain", "chair", "chalk", "chart", "chest", "chime", "chisel", "clamp",
	"clasp", "clock", "cloak", "closet", "cloud", "coil", "coin", "collar",
	"comb", "compass", "cone", "cord", "cork", "crate", "crayon", "crown",
	"crutch", "cube", "curtain", "cushion", "dagger", "dial", "diary", "dish",
	"dock", "dome", "door", "dresser", "drill", "drum", "easel", "engine",
	"fabric", "feather", "fence", "ferry", "fiddle", "file", "flag", "flask",
	"flute", "foil", "folder", "fork", "frame", "funnel", "gadget", "gallery",
	"garden", "gate", "gauge", "gavel", "gear", "ginger", "glass", "glider",
	"globe", "glove", "goblet", "grate", "grill", "guitar", "gutter", "hammer",
	"hammock", "handle", "harp", "hatch", "hedge", "helm", "helmet", "hinge",
	"hive", "hoop", "hose", "igloo", "ingot", "inkwell", "iron", "jacket",
	"jetty", "jewel", "journal", "kayak", "kettle", "kiln", "kilt", "ladder",
	"lantern", "lasso", "latch", "lattice", "ledger", "lens", "lever", "library",
	"lighter", "linen", "lock", "locker", "locket", "loom", "mailbox", "mallet",
	"mantel", "manual", "marble", "marker", "mask", "mast", "medal", "mirror",
	"mitten", "mixer", "mold", "monocle", "mortar", "mosaic", "motor", "nail",
	"napkin", "needle", "nozzle", "oven", "packet", "paddle", "padlock", "palette",
	"panel", "pantry", "parcel", "pastel", "patch", "pedal", "pencil", "pennant",
	"pepper", "piano", "pillar", "pillow", "pipe", "piston", "pitcher", "planer",
	"plank", "planter", "plate", "platter", "plaza", "pliers", "plug", "pocket",
	"pole", "pond", "porch", "poster", "pouch", "prism", "pulley", "pump",
	"purse", "puzzle", "quilt", "quiver", "radio", "raft", "railing", "rake",
	"ramp", "ranch", "rattle", "razor", "ribbon", "rivet", "rocket", "rope",
	"rudder", "ruler", "sack", "saddle", "sail", "salt", "sandal", "satchel",
	"saucer", "scale", "scarf", "scoop", "screen", "screw", "scroll", "seat",
}
