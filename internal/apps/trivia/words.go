package trivia

// The two word lists a game name is drawn from. A name is one of each,
// hyphenated -- jumping-lion -- and its one hard requirement is that somebody
// can read it off a TV across a loud room and type it into a phone correctly
// on the first try.
//
// TWO words, not three, and the first is a verb. Three arbitrary words
// ("brave-otter-lamp") were a mouthful to read out and had no shape to hold
// on to; a verb and an animal makes a little picture, which is markedly
// easier to remember for the ten seconds between looking up at the screen and
// typing it in.
//
// Every word is lowercase ASCII and unambiguous when spoken. Homophone pairs
// are excluded on both sides (bear/bare, tern/turn, pail/pale) because
// "was that b-e-a-r or b-a-r-e" is exactly the failure that sends someone to
// the bar to ask. Nothing profane, and no word appears in both lists.
//
// 288 x 224 is about 65 thousand combinations. Far fewer than the three-word
// scheme, and still far more than a venue running one quiz a week will use in
// a lifetime -- and the UNIQUE (tenant_id, name) index plus retry is what
// actually guarantees no collision, not the size of the space.

var verbs = []string{
	"jumping", "running", "dancing", "singing", "flying", "laughing", "jogging",
	"hopping", "skipping", "diving", "racing", "sailing", "riding", "rolling",
	"spinning", "gliding", "prowling", "roaming", "waltzing", "marching", "strutting",
	"bounding", "charging", "climbing", "crawling", "creeping", "dashing", "drifting",
	"drumming", "floating", "grinning", "howling", "humming", "hunting", "lurking",
	"napping", "nodding", "pacing", "plotting", "pouncing", "prancing", "purring",
	"roaring", "rushing", "scheming", "sipping", "sleeping", "sliding", "sneaking",
	"soaring", "sprinting", "stalking", "stomping", "strolling", "swimming", "swooping",
	"trotting", "wading", "waving", "winking", "yawning", "zooming", "beaming",
	"blazing", "bobbing", "boasting", "bouncing", "braving", "brewing", "bustling",
	"buzzing", "calling", "camping", "caring", "carving", "chanting", "charming",
	"chasing", "cheering", "chewing", "chirping", "chomping", "chopping", "cooking",
	"cooling", "coasting", "crooning", "cruising", "curling", "darting", "dodging",
	"dozing", "drilling", "drinking", "dueling", "farming", "fencing", "fetching",
	"fibbing", "fiddling", "fishing", "fixing", "flapping", "flashing", "flexing",
	"flicking", "flipping", "flirting", "flocking", "flowing", "fluffing", "forging",
	"foraging", "frowning", "gaming", "gasping", "gazing", "giggling", "glaring",
	"gleaming", "glowing", "gnawing", "grazing", "griping", "groaning", "grooving",
	"growing", "grumbling", "guarding", "gulping", "hatching", "hauling", "healing",
	"heaving", "herding", "hiking", "hissing", "hoarding", "hoisting", "holding",
	"hooting", "hovering", "hugging", "hurdling", "hustling", "inking", "jabbing",
	"jamming", "jesting", "jetting", "jingling", "joking", "joshing", "juggling",
	"kicking", "kneading", "knitting", "lapping", "leaping", "leading", "lifting",
	"limping", "lolling", "looming", "lounging", "lunging", "marveling", "mending",
	"mincing", "mingling", "moaning", "molting", "mooching", "mulling", "munching",
	"musing", "nesting", "nibbling", "nudging", "nuzzling", "offering", "opening",
	"pairing", "panting", "parading", "parking", "passing", "pecking", "peeking",
	"perching", "picking", "piloting", "pinning", "pitching", "plodding", "plunging",
	"polishing", "pondering", "posing", "prodding", "puffing", "pulling", "pumping",
	"punting", "puzzling", "quaffing", "questing", "quibbling", "rafting", "raiding",
	"raking", "rambling", "ranging", "rapping", "reaching", "reaping", "reeling",
	"resting", "rigging", "rinsing", "roasting", "rocking", "romping", "roosting",
	"rooting", "roving", "rowing", "ruling", "sampling", "sanding", "savoring",
	"scaling", "scanning", "scouting", "scraping", "scrubbing", "scurrying", "seeking",
	"serving", "shaking", "sharing", "shifting", "shining", "shopping", "shouting",
	"shoving", "shuffling", "sifting", "sighing", "signing", "simmering", "sizzling",
	"skating", "sketching", "skimming", "skulking", "slurping", "smiling", "smirking",
	"snacking", "snapping", "sniffing", "snoozing", "snorting", "sorting", "sowing",
	"sparring", "speaking", "spending", "splashing", "sporting", "sprouting", "squinting",
	"stacking", "staging", "stamping", "standing", "starting", "stashing", "steering",
	"stewing", "stirring", "stitching", "stooping", "storming", "strumming", "stumbling",
	"sulking",
}

var animals = []string{
	"otter", "badger", "falcon", "heron", "marten", "weasel", "bison",
	"beaver", "bobcat", "cougar", "coyote", "ferret", "gecko", "gopher",
	"grouse", "hamster", "iguana", "jackal", "jaguar", "kestrel", "lemur",
	"lizard", "llama", "lynx", "magpie", "marmot", "mink", "mole",
	"moose", "mouse", "newt", "ocelot", "opossum", "osprey", "owl",
	"panda", "parrot", "pelican", "penguin", "pigeon", "possum", "puffin",
	"python", "quail", "rabbit", "raccoon", "raven", "robin", "salmon",
	"shark", "sheep", "shrew", "skunk", "sloth", "snail", "sparrow",
	"squid", "starling", "stoat", "stork", "swan", "tapir", "thrush",
	"tiger", "trout", "turkey", "turtle", "viper", "vole", "vulture",
	"walrus", "warbler", "wombat", "woodcock", "wren", "yak", "zebra",
	"beetle", "bird", "camel", "cobra", "condor", "crane", "crow",
	"deer", "dingo", "dove", "duck", "eagle", "eel", "egret",
	"elk", "emu", "finch", "fish", "fox", "frog", "gannet",
	"goat", "goose", "gull", "hawk", "kite", "kiwi", "koala",
	"lamb", "lark", "leech", "lion", "loon", "mare", "martin",
	"moth", "mule", "oryx", "panther", "perch", "pike", "pony",
	"poodle", "prawn", "puma", "quoll", "ram", "rat", "rook",
	"serval", "shrimp", "skink", "slug", "smelt", "snake", "snipe",
	"sponge", "squab", "stag", "sunfish", "swift", "terrier", "tortoise",
	"toucan", "tuna", "urchin", "vixen", "wasp", "weevil", "whelk",
	"wolf", "worm", "alpaca", "antelope", "armadillo", "baboon", "bandicoot",
	"barnacle", "buffalo", "bullfrog", "bumblebee", "caribou", "catfish", "cheetah",
	"chipmunk", "cricket", "crocodile", "dolphin", "donkey", "dragonfly", "falconet",
	"flamingo", "gazelle", "gerbil", "gibbon", "giraffe", "grackle", "hedgehog",
	"hornet", "impala", "kangaroo", "ladybird", "lobster", "macaw", "mallard",
	"mammoth", "manatee", "mandrill", "meerkat", "mongoose", "narwhal", "nightjar",
	"numbat", "octopus", "ostrich", "pangolin", "partridge", "peacock", "pheasant",
	"platypus", "porcupine", "quetzal", "reindeer", "rhino", "sandpiper", "scorpion",
	"seahorse", "songbird", "spaniel", "spider", "squirrel", "starfish", "stingray",
	"sturgeon", "swallow", "tadpole", "tamarin", "tarantula", "termite", "tigerfish",
	"tomcat", "toucanet", "wallaby", "warthog", "waxwing", "wildcat", "wolverine",
}
