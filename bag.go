/*
Package for working with files stored using the BagIt specification (see below).

It facilitates the creation of bags, adding files to the bag payload and managing
checksums for the file manifest as well as data stored in tag files.

For more information on Bag tagfiles see
http://tools.ietf.org/html/draft-kunze-bagit-09#section-2.3
*/
package bagins

/*

“He that breaks a thing to find out what it is has left the path of wisdom.”

- Gandalf the Grey

*/

import (
	"github.com/APTrust/bagins/bagutil"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Represents the basic structure of a bag which is controlled by methods.
type Bag struct {
	pathToFile      string // path to the bag
	payload         *Payload
	Manifests       []*Manifest
	tagfiles        map[string]*TagFile // Key is relative path
}

// METHODS FOR CREATING AND INITALIZING BAGS

/*
 Creates a new bag under the location directory and creates a bag root directory
 with the provided name.  Returns an error if the location does not exist or if the
 bag already exist.

 This constructor will automatically create manifests with the
 specified hash algorithms. Supported algorithms include:

 "md5", "sha1", "sha256", "sha512", "sha224" and "sha384"

 If param createTagManifests is true, this will also create tag manifests
 with the specified algorithms.

 example:
		NewBag("archive/bags", "bag-34323", ["sha256", "md5"], true)
*/
func NewBag(location string, name string, hashNames []string, createTagManifests bool) (*Bag, error) {
	// Create the bag object.
	bag := new(Bag)

	if bag.Manifests == nil {
		bag.Manifests = make([]*Manifest, 0)
	}

	// Start with creating the directories.
	bag.pathToFile = filepath.Join(location, name)
	err := os.Mkdir(bag.pathToFile, 0755)
	if err != nil {
		return nil, err
	}
	//defer bag.Save()

	// Init the manifests and tag manifests
	for _, hashName := range hashNames {
		lcHashName := strings.ToLower(hashName)
		manifest, err := NewManifest(bag.Path(), lcHashName)
		if err != nil {
			return nil, err
		}
		bag.Manifests = append(bag.Manifests, manifest)

		if createTagManifests == true {
			tagManifestName := fmt.Sprintf("tagmanifest-%s.txt", lcHashName)
			fullPath := filepath.Join(bag.Path(), tagManifestName)
			tagmanifest, err := NewManifest(fullPath, lcHashName)
			if err != nil {
				return nil, err
			}
			bag.Manifests = append(bag.Manifests, tagmanifest)
		}
	}

	// Init the payload directory and such.
	plPath := filepath.Join(bag.Path(), "data")
	err = os.Mkdir(plPath, 0755)
	if err != nil {
		return nil, err
	}
	bag.payload, err = NewPayload(plPath)
	if err != nil {
		return nil, err
	}

	// Init tagfiles map and create the BagIt.txt Tagfile
	bag.tagfiles = make(map[string]*TagFile)
	tf, err := bag.createBagItFile()
	if err != nil {
		return nil, err
	}
	bag.tagfiles["bagit.txt"] = tf

	errors := bag.Save()
	if err != nil && len(errors) > 0 {
		message := ""
		for _, e := range errors {
			message = fmt.Sprintf("%s, %s", message, e.Error())
		}
		return nil, fmt.Errorf(message)
	}

	return bag, nil
}

// Creates the required bagit.txt file as per the specification
// http://tools.ietf.org/html/draft-kunze-bagit-09#section-2.1.1
func (b *Bag) createBagItFile() (*TagFile, error) {
	if err := b.AddTagfile("bagit.txt"); err != nil {
		return nil, err
	}
	bagit, err := b.TagFile("bagit.txt")
	if err != nil {
		return nil, err
	}
	bagit.Data.AddField(*NewTagField("BagIt-Version", "0.97"))
	bagit.Data.AddField(*NewTagField("Tag-File-Character-Encoding", "UTF-8"))

	return bagit, nil
}

/*
	Reads the directory provided as the root of a new bag and attemps to parse the file
	contents into payload, manifests and tagfiles.
*/
func ReadBag(pathToFile string, tagfiles []string) (*Bag, error) {
	// validate existance
	fi, err := os.Stat(pathToFile)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory.", pathToFile)
	}

	// Get the payload directory.
	payload, err := NewPayload(filepath.Join(pathToFile, "data"))
	if err != nil {
		return nil, err
	}

	// Get the bag root directory.
	bag := new(Bag)
	bag.pathToFile = pathToFile
	bag.payload = payload
	bag.tagfiles = make(map[string]*TagFile)

	errors := bag.findManifests()
	if errors != nil {
		errorMessage := ""
		for _, e := range errors {
			errorMessage = fmt.Sprintf("%s; %s", errorMessage, e.Error())
		}
		return nil, fmt.Errorf(errorMessage)
	}
	if len(bag.Manifests) == 0 {
		return nil, fmt.Errorf("Unable to parse a manifest")
	}

	for i := range bag.Manifests {
		manifest := bag.Manifests[i]
		manifestPath := manifest.Name()
		if filepath.Dir(manifestPath) != bag.pathToFile {
			manifestPath = filepath.Join(bag.pathToFile, manifest.Name())
		}
		if _, err := os.Stat(manifestPath); err != nil {
			return nil, fmt.Errorf("Can't find manifest: %v", err)
		}
		parsedManifest, errs := ReadManifest(manifestPath)
		if errs != nil && len(errs) > 0 {
			errors := ""
			for _, e := range(errs) {
				errors = fmt.Sprintf("%s; %s", errors, e.Error())
			}
			return nil, fmt.Errorf("Unable to parse manifest %s: %s", manifestPath, errors)
		} else {
			bag.Manifests[i] = parsedManifest
		}
	}

	/*
       Note that we are parsing tags from the expected tag files, and
       not parsing tags from unexpected tag files. This is per the BagIt
       spec for V0.97, section 2.2.4, as described here:

       http://tools.ietf.org/html/draft-kunze-bagit-13#section-2.2.4

       A bag MAY contain other tag files that are not defined by this
       specification.  Implementations SHOULD ignore the content of any
	   unexpected tag files, except when they are listed in a tag manifest.
       When unexpected tag files are listed in a tag manifest,
       implementations MUST only treat the content of those tag files as
       octet streams for the purpose of checksum verification.
    */
	for _, tName := range tagfiles {
		tf, errs := ReadTagFile(filepath.Join(bag.pathToFile, tName))
		// Warn on Stderr only if we're running as bagmaker
		if len(errs) != 0 && strings.Index(os.Args[0], "bagmaker") > -1 {
			log.Println("While parsing tagfiles:", errs)
		}
		if tf != nil {
			bag.tagfiles[tName] = tf
		}
	}

	return bag, nil
}

// Finds all payload and tag manifests in an existing bag.
// This is used by ReadBag, not when creating a bag.
func (b *Bag) findManifests() ([]error){
	if b.Manifests == nil {
		b.Manifests = make([]*Manifest, 0)
	}
	if len(b.Manifests) == 0 {
		bagFiles, _ := b.ListFiles()
		for _, fName := range bagFiles {

			filePath := filepath.Join(b.pathToFile, fName)
			payloadManifestPrefix := filepath.Join(b.pathToFile, "manifest-")
			tagManifestPrefix := filepath.Join(b.pathToFile, "tagmanifest-")

			if strings.HasPrefix(filePath, payloadManifestPrefix) ||
				strings.HasPrefix(filePath, tagManifestPrefix) {
				manifest, errors := ReadManifest(filePath)
				if errors != nil && len(errors) > 0 {
					return errors
				}
				b.Manifests = append(b.Manifests, manifest)
			}
		}
	}
	return nil
}

// METHODS FOR MANAGING BAG PAYLOADS

/*
  Adds a file specified by src parameter to the data directory under
  the relative path and filename provided in the dst parameter.
  example:
			err := b.AddFile("/tmp/myfile.txt", "myfile.txt")
*/
func (b *Bag) AddFile(src string, dst string) error {
	payloadManifests := b.GetManifests(PayloadManifest)
	_, err := b.payload.Add(src, dst, payloadManifests)
	if err != nil {
		return err
	}
	return err
}

// Performans a Bag.AddFile on all files found under the src location including all
// subdirectories.
// example:
//			errs := b.AddDir("/tmp/mypreservationfiles")
func (b *Bag) AddDir(src string) (errs []error) {
	payloadManifests := b.GetManifests(PayloadManifest)
	_, errs = b.payload.AddAll(src, payloadManifests)
	return errs
}

// METHODS FOR MANAGING BAG TAG FILES

/*
 Adds a tagfile to the bag with the filename provided,
 creating whatever subdirectories are needed if supplied
 as part of name parameter.
 example:
			err := b.AddTagfile("baginfo.txt")
*/
func (b *Bag) AddTagfile(name string) error {
	tagFilePath := filepath.Join(b.Path(), name)
	if err := os.MkdirAll(filepath.Dir(tagFilePath), 0766); err != nil {
		return err
	}
	tf, err := NewTagFile(tagFilePath)
	if err != nil {
		return err
	}
	b.tagfiles[name] = tf
	if err := tf.Create(); err != nil {
		return err
	}
	return nil
}

/*
 Finds a tagfile in by its relative path to the bag root directory.
 example:
			tf, err := b.TagFile("bag-info.txt")
*/
func (b *Bag) TagFile(name string) (*TagFile, error) {
	if tf, ok := b.tagfiles[name]; ok {
		return tf, nil
	}
	return nil, fmt.Errorf("Unable to find tagfile %s", name)
}

/*
  Lists all the current tag files the bag is tracking.
*/
func (b *Bag) ListTagFiles() []string {
	names := make([]string, len(b.tagfiles))
	i := 0
	for k, _ := range b.tagfiles {
		names[i] = k
		i++
	}
	return names
}

/*
 Convienence method to return the bag-info.txt tag file if it exists.  Since
 this is optional it will not be created by default and will return an error
 if you have not defined or added it yourself via Bag.AddTagfile
*/
func (b *Bag) BagInfo() (*TagFile, error) {
	tf, err := b.TagFile("bag-info.txt")
	if err != nil {
		return nil, err
	}
	return tf, nil
}


// Returns the manifest with the specified algorithm and type,
// or nil. For example, GetManifest(PayloadManifest, "sha256")
// returns either a reference to manifest-sha256.txt or nil.
// GetManifest(TagManifest, "md5") returns a reference to
// tagmanifest-md5.txt or nil.
func (b *Bag) GetManifest(manifestType, algorithm string) (*Manifest) {
	for _, m := range b.Manifests {
		if m.Type() == manifestType && m.Algorithm() == algorithm {
			return m
		}
	}
	return nil
}

// Returns the manifests of the specified type,
// or an empty slice. For example, GetManifests(PayloadManifest)
// returns all of the payload manifests.
func (b *Bag) GetManifests(manifestType string) ([]*Manifest) {
	manifests := make([]*Manifest, 0)
	for _, m := range b.Manifests {
		if m.Type() == manifestType {
			manifests = append(manifests, m)
		}
	}
	return manifests
}

// TODO create methods for managing fetch file.

// METHODS FOR MANAGING OR RETURNING INFORMATION ABOUT THE BAG ITSELF

// Returns the full path of the bag including it's own directory.
func (b *Bag) Path() string {
	return b.pathToFile
}

/*
 This method writes all the relevant tag and manifest files to finish off the
 bag.
*/
func (b *Bag) Save() (errs []error) {
	// Write all the tag files.
	tagManifests := b.GetManifests(TagManifest)
	for _, tf := range b.tagfiles {
		if err := os.MkdirAll(filepath.Dir(tf.Name()), 0766); err != nil {
			errs = append(errs, err)
		}
		if err := tf.Create(); err != nil {
			errs = append(errs, err)
		}

		// Add tag file checksums to tag manifests
		for i := range tagManifests {
			manifest := tagManifests[i]
			checksum, err := bagutil.FileChecksum(tf.Name(), manifest.hashFunc())
			if err != nil {
				errors := []error {
					fmt.Errorf("Error calculating %s checksum for file %s: %v",
						manifest.Algorithm(), tf.Name(), err),
				}
				return errors
			}
			manifest.Data[tf.Name()] = checksum
		}
	}

	// Write all the manifest files.
	for i := range b.Manifests {
		manifest := b.Manifests[i]
		if err := manifest.Create(); err != nil {
			errs = append(errs, err)
		}
	}
	return
}

/*
 Walks the bag directory and subdirectories and returns the
 filepaths found inside and any errors skipping files in the
 payload directory.
*/
func (b *Bag) ListFiles() ([]string, error) {

	var files []string

	// WalkDir function to collect files in the bag..
	visit := func(pathToFile string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		isPayload := strings.HasPrefix(pathToFile, b.payload.Name())
		if !info.IsDir() || !isPayload {
			fp, err := filepath.Rel(b.Path(), pathToFile)
			if err != nil {
				return err
			}
			if fp != "." {
				files = append(files, fp)
			}
		}
		return err
	}

	if err := filepath.Walk(b.Path(), visit); err != nil {
		return nil, err
	}

	return files, nil
}
